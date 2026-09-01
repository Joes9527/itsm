package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketattachment"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

type TicketAttachmentService struct {
	client       *ent.Client
	logger       *zap.SugaredLogger
	storage      AttachmentStorage
	maxFileSize  int64    // 最大文件大小（字节），默认10MB
	allowedTypes []string // 允许的文件类型
	virusScanner AttachmentVirusScanner
}

type AttachmentVirusScanner interface {
	Scan(context.Context, io.Reader, int64) error
}
type noopAttachmentVirusScanner struct{}

func (noopAttachmentVirusScanner) Scan(context.Context, io.Reader, int64) error { return nil }

func NewTicketAttachmentService(client *ent.Client, logger *zap.SugaredLogger) *TicketAttachmentService {
	return &TicketAttachmentService{
		client:  client,
		logger:  logger,
		storage: NewLocalAttachmentStorage("uploads"),
		maxFileSize: 10 * 1024 * 1024, // 10MB
		allowedTypes: []string{
			// 图片
			"image/jpeg", "image/png", "image/gif", "image/webp",
			// 文档
			"application/pdf",
			"application/msword",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.ms-excel",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"application/vnd.ms-powerpoint",
			"application/vnd.openxmlformats-officedocument.presentationml.presentation",
			// 文本
			"text/plain", "text/csv",
			// 压缩文件
			"application/zip", "application/x-rar-compressed",
		},
		virusScanner: noopAttachmentVirusScanner{},
	}
}

// SetStorage 切换附件存储后端（默认本地文件系统，可切换为 MinIO）。
func (s *TicketAttachmentService) SetStorage(storage AttachmentStorage) {
	if storage != nil {
		s.storage = storage
	}
}

func (s *TicketAttachmentService) SetVirusScanner(scanner AttachmentVirusScanner) {
	if scanner != nil {
		s.virusScanner = scanner
	}
}

// UploadAttachment 上传附件
func (s *TicketAttachmentService) UploadAttachment(
	ctx context.Context,
	ticketID int,
	fileHeader *FileHeader,
	userID, tenantID int,
) (*dto.TicketAttachmentResponse, error) {
	s.logger.Infow("Uploading attachment", "ticket_id", ticketID, "file_name", fileHeader.Filename, "user_id", userID)

	// 验证工单是否存在且属于当前租户
	ticketExists, err := s.client.Ticket.Query().
		Where(
			ticket.ID(ticketID),
			ticket.TenantID(tenantID),
		).
		Exist(ctx)
	if err != nil {
		s.logger.Errorw("Failed to check ticket existence", "error", err)
		return nil, fmt.Errorf("failed to check ticket existence: %w", err)
	}
	if !ticketExists {
		return nil, fmt.Errorf("ticket not found")
	}

	// 验证文件大小
	if fileHeader.Size > s.maxFileSize {
		return nil, fmt.Errorf("file size exceeds maximum allowed size (%d bytes)", s.maxFileSize)
	}

	// 验证文件类型（Client-provided Content-Type 可被伪造，仅作初筛）
	mimeType := fileHeader.ContentType
	if mimeType == "" {
		// 尝试从文件扩展名推断
		ext := filepath.Ext(fileHeader.Filename)
		mimeType = mime.TypeByExtension(ext)
	}

	if !s.isAllowedType(mimeType) {
		return nil, fmt.Errorf("file type not allowed: %s", mimeType)
	}

	// 1) 文件名清洗：拒绝路径遍历/控制字符，限制长度，防止 XSS/覆盖/目录穿越
	safeName := sanitizeFilename(fileHeader.Filename)
	if safeName == "" {
		return nil, fmt.Errorf("invalid filename: empty after sanitization")
	}

	// 2) Magic bytes / 实际内容嗅探：读入内存并检测真实类型，避免 Content-Type/扩展名与真实内容不一致
	var data []byte
	if fileHeader.Reader != nil {
		data, err = io.ReadAll(io.LimitReader(fileHeader.Reader, s.maxFileSize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
		if int64(len(data)) > s.maxFileSize {
			return nil, fmt.Errorf("file size exceeds maximum allowed size (%d bytes)", s.maxFileSize)
		}
		if len(data) > 0 {
			detected := http.DetectContentType(data)
			if !s.isAllowedType(detected) {
				return nil, fmt.Errorf("detected file type not allowed: %s (claimed: %s)", detected, mimeType)
			}
			mimeType = detected
		}
	}

	// 生成唯一 object key（使用清洗后的文件名）
	fileName := fmt.Sprintf("%d_%d_%s", ticketID, time.Now().UnixNano(), safeName)
	key := fmt.Sprintf("tickets/%s", fileName)

	// 病毒扫描（对内容字节流，存储后端无关）
	if err := s.virusScanner.Scan(ctx, bytes.NewReader(data), int64(len(data))); err != nil {
		return nil, fmt.Errorf("file rejected by malware scan")
	}

	// 保存到存储后端（本地或 MinIO）
	if err := s.storage.Save(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
		s.logger.Errorw("Failed to save file", "error", err)
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	// 生成文件URL（相对路径，实际URL由前端或CDN提供）
	fileURL := fmt.Sprintf("/api/v1/tickets/%d/attachments/%s/download", ticketID, fileName)

	// 创建附件记录
	attachment, err := s.client.TicketAttachment.Create().
		SetTicketID(ticketID).
		SetFileName(safeName).
		SetFilePath(key).
		SetFileURL(fileURL).
		SetFileSize(int(fileHeader.Size)).
		SetFileType(mimeType).
		SetMimeType(mimeType).
		SetUploadedBy(userID).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		// 如果数据库保存失败，删除已上传的文件
		_ = s.storage.Delete(ctx, key)
		s.logger.Errorw("Failed to create attachment record", "error", err)
		return nil, fmt.Errorf("failed to create attachment record: %w", err)
	}

	// 查询上传人信息
	uploader, err := s.client.User.Get(ctx, userID)
	if err != nil {
		s.logger.Warnw("Failed to get uploader", "error", err, "user_id", userID)
		uploader = nil
	}

	return dto.ToTicketAttachmentResponse(attachment, uploader), nil
}

// ListAttachments 获取附件列表
func (s *TicketAttachmentService) ListAttachments(ctx context.Context, ticketID, tenantID, userID int) ([]*dto.TicketAttachmentResponse, error) {
	s.logger.Infow("Listing attachments", "ticket_id", ticketID)
	if err := s.authorizeTicketAttachmentAccess(ctx, ticketID, tenantID, userID); err != nil {
		return nil, err
	}

	// 验证工单是否存在且属于当前租户
	ticketExists, err := s.client.Ticket.Query().
		Where(
			ticket.ID(ticketID),
			ticket.TenantID(tenantID),
		).
		Exist(ctx)
	if err != nil {
		s.logger.Errorw("Failed to check ticket existence", "error", err)
		return nil, fmt.Errorf("failed to check ticket existence: %w", err)
	}
	if !ticketExists {
		return nil, fmt.Errorf("ticket not found")
	}

	// 查询附件
	attachments, err := s.client.TicketAttachment.Query().
		Where(
			ticketattachment.TicketID(ticketID),
			ticketattachment.TenantID(tenantID),
		).
		Order(ent.Desc(ticketattachment.FieldCreatedAt)).
		WithUploader().
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list attachments", "error", err)
		return nil, fmt.Errorf("failed to list attachments: %w", err)
	}

	// 转换为 DTO
	responses := make([]*dto.TicketAttachmentResponse, 0, len(attachments))
	for _, attachment := range attachments {
		var uploader *ent.User
		if attachment.Edges.Uploader != nil {
			uploader = attachment.Edges.Uploader
		} else {
			uploader, _ = s.client.User.Get(ctx, attachment.UploadedBy)
		}
		responses = append(responses, dto.ToTicketAttachmentResponse(attachment, uploader))
	}

	return responses, nil
}

// GetAttachment 获取附件信息
func (s *TicketAttachmentService) GetAttachment(ctx context.Context, ticketID, attachmentID, tenantID int) (*dto.TicketAttachmentResponse, error) {
	attachment, err := s.client.TicketAttachment.Query().
		Where(
			ticketattachment.ID(attachmentID),
			ticketattachment.TicketID(ticketID),
			ticketattachment.TenantID(tenantID),
		).
		WithUploader().
		Only(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get attachment", "error", err)
		return nil, fmt.Errorf("attachment not found: %w", err)
	}

	var uploader *ent.User
	if attachment.Edges.Uploader != nil {
		uploader = attachment.Edges.Uploader
	} else {
		uploader, _ = s.client.User.Get(ctx, attachment.UploadedBy)
	}

	return dto.ToTicketAttachmentResponse(attachment, uploader), nil
}

// DeleteAttachment 删除附件
func (s *TicketAttachmentService) DeleteAttachment(ctx context.Context, ticketID, attachmentID, tenantID, userID int) error {
	s.logger.Infow("Deleting attachment", "ticket_id", ticketID, "attachment_id", attachmentID, "user_id", userID)

	// 查询附件
	attachment, err := s.client.TicketAttachment.Query().
		Where(
			ticketattachment.ID(attachmentID),
			ticketattachment.TicketID(ticketID),
			ticketattachment.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get attachment", "error", err)
		return fmt.Errorf("attachment not found: %w", err)
	}

	// 权限检查：只有上传人或工单处理人可以删除
	ticketInfo, err := s.client.Ticket.Query().
		Where(
			ticket.ID(ticketID),
			ticket.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get ticket", "error", err)
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	canDelete := attachment.UploadedBy == userID ||
		(ticketInfo.AssigneeID > 0 && ticketInfo.AssigneeID == userID) ||
		ticketInfo.RequesterID == userID
	if !canDelete {
		return fmt.Errorf("permission denied: only uploader, ticket assignee or requester can delete")
	}

	// 删除文件（存储后端：本地或 MinIO）
	if err := s.storage.Delete(ctx, attachment.FilePath); err != nil {
		s.logger.Warnw("Failed to delete file", "error", err, "key", attachment.FilePath)
		// 继续删除数据库记录，即使文件删除失败
	}

	// 删除数据库记录
	err = s.client.TicketAttachment.DeleteOneID(attachmentID).
		Where(
			ticketattachment.TicketID(ticketID),
			ticketattachment.TenantID(tenantID),
		).
		Exec(ctx)
	if err != nil {
		s.logger.Errorw("Failed to delete attachment", "error", err)
		return fmt.Errorf("failed to delete attachment: %w", err)
	}

	return nil
}

// GetAttachmentFile 获取附件文件（用于下载）
func (s *TicketAttachmentService) GetAttachmentFile(ctx context.Context, ticketID, attachmentID, tenantID, userID int) (*AttachmentFile, error) {
	if err := s.authorizeTicketAttachmentAccess(ctx, ticketID, tenantID, userID); err != nil {
		return nil, err
	}
	attachment, err := s.client.TicketAttachment.Query().
		Where(
			ticketattachment.ID(attachmentID),
			ticketattachment.TicketID(ticketID),
			ticketattachment.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("attachment not found: %w", err)
	}

	file, _, err := s.storage.Open(ctx, attachment.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	mimeType := attachment.MimeType
	if mimeType == "" {
		mimeType = attachment.FileType
	}
	return &AttachmentFile{
		File:     file,
		FileName: attachment.FileName,
		MimeType: &mimeType,
		Size:     int64(attachment.FileSize),
	}, nil
}

// 辅助方法

// FileHeader 文件头信息
type FileHeader struct {
	Filename    string
	Size        int64
	ContentType string
	Reader      io.Reader
}

// AttachmentFile 附件文件
type AttachmentFile struct {
	File     io.ReadCloser
	FileName string
	MimeType *string
	Size     int64
}

// saveFile 已移除：文件保存统一走 AttachmentStorage 抽象（本地或 MinIO）。

func (s *TicketAttachmentService) authorizeTicketAttachmentAccess(ctx context.Context, ticketID, tenantID, userID int) error {
	if userID <= 0 {
		return fmt.Errorf("authentication required")
	}
	t, err := s.client.Ticket.Query().Where(ticket.ID(ticketID), ticket.TenantID(tenantID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("ticket not found")
	}
	if t.RequesterID == userID || (t.AssigneeID > 0 && t.AssigneeID == userID) {
		return nil
	}
	u, err := s.client.User.Query().Where(user.ID(userID), user.TenantID(tenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return fmt.Errorf("permission denied")
	}
	// 内部角色（非 end_user / guest）允许操作附件
	if u.Role != "" && u.Role != "end_user" && u.Role != "guest" {
		return nil
	}
	return fmt.Errorf("permission denied")
}

func SanitizeDownloadFilename(name string) string { return sanitizeFilename(name) }

// isAllowedType 检查文件类型是否允许
func (s *TicketAttachmentService) isAllowedType(mimeType string) bool {
	if mimeType == "" {
		return false
	}

	// 规范化：去掉 "; charset=utf-8" 等参数，取纯 MIME 类型。
	// http.DetectContentType 对 UTF-8 文本会返回 "text/plain; charset=utf-8"，
	// 而白名单存的是 "text/plain"，不规范化会导致合法文本附件被拒绝。
	if mediatype, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = mediatype
	}

	// 检查精确匹配
	for _, allowed := range s.allowedTypes {
		if mimeType == allowed {
			return true
		}
	}

	// 检查类型前缀（如 image/*, application/*）
	parts := strings.Split(mimeType, "/")
	if len(parts) == 2 {
		typePrefix := parts[0] + "/*"
		for _, allowed := range s.allowedTypes {
			if allowed == typePrefix {
				return true
			}
		}
	}

	return false
}

// sanitizeFilename cleans an upload filename for safe on-disk + header usage.
// - disallows path separators, control chars, NUL, leading dots, relative segments
// - limits length to 200 runes
func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	// 路径遍历防御：剥掉任何目录部分
	name = filepath.Base(name)
	// 去掉 Windows 驱动器前缀和反斜杠路径段
	if strings.ContainsRune(name, '\\') {
		parts := strings.FieldsFunc(name, func(r rune) bool { return r == '\\' })
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
	}
	// 剥掉控制字符、NUL、以及可能触发 shell/URL 二次解析的危险字符
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			continue // control / DEL
		case r == '/' || r == '\\' || r == 0:
			continue
		case r == '%' || r == '`' || r == '|' || r == '&' || r == ';' || r == '>' || r == '<' || r == '"' || r == '\'' || r == '*' || r == '?':
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	out = strings.TrimLeft(out, ". ") // 防 ../ 和 dotfiles
	if out == "" || out == "." || out == ".." {
		return ""
	}
	// 截断到 200 runes
	runes := []rune(out)
	if len(runes) > 200 {
		out = string(runes[:200])
	}
	return out
}

// prefixedReader 已移除：上传改为一次性读入内存后统一做类型嗅探与存储。
