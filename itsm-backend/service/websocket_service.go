package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"itsm-backend/dto"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// GetAllowedOrigins returns allowed origins from environment variable, defaults to same-origin only
func getAllowedOrigins() map[string]bool {
	origins := make(map[string]bool)
	originsEnv := os.Getenv("WEBSOCKET_ALLOWED_ORIGINS")
	if originsEnv == "" || originsEnv == "*" {
		// Default: only allow same-origin connections for security
		return origins
	}
	// Parse comma-separated list of allowed origins
	for _, origin := range splitAndTrim(originsEnv, ",") {
		origins[origin] = true
	}
	return origins
}

func splitAndTrim(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := make([]string, 0)
	start := 0
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			if part := trimString(s[start:i]); part != "" {
				parts = append(parts, part)
			}
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	if part := trimString(s[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func trimString(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		allowedOrigins := getAllowedOrigins()
		// If no origins configured (default), only allow same-origin
		if len(allowedOrigins) == 0 {
			origin := r.Header.Get("Origin")
			// Allow same-origin requests
			if origin == "" {
				return true // curl or direct connection
			}
			// Check if origin matches host
			return origin == "http://"+r.Host || origin == "https://"+r.Host
		}
		// Check against allowed origins list
		origin := r.Header.Get("Origin")
		return allowedOrigins[origin]
	},
}

// WebSocketMessage WebSocket消息
type WebSocketMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

var errPushNotAccepted = errors.New("push_not_accepted")
var errPushUnknown = errors.New("push_delivery_unknown")

type websocketFrame struct {
	payload []byte
	receipt chan<- error
	ctx     context.Context
}

func (f websocketFrame) finish(err error) {
	if f.receipt != nil {
		select {
		case f.receipt <- err:
		default:
		}
	}
}

// WebSocketClient WebSocket客户端
type WebSocketClient struct {
	ID       string
	UserID   int
	TenantID int
	Conn     *websocket.Conn
	Send     chan websocketFrame
	Hub      *WebSocketHub
	IsClosed bool
}

// WebSocketHub WebSocket中心
type WebSocketHub struct {
	clients    map[*WebSocketClient]bool
	broadcast  chan []byte
	register   chan *WebSocketClient
	unregister chan *WebSocketClient
	logger     *zap.SugaredLogger
	mu         sync.RWMutex
}

// NewWebSocketHub 创建WebSocket中心
func NewWebSocketHub(logger *zap.SugaredLogger) *WebSocketHub {
	return &WebSocketHub{
		clients:    make(map[*WebSocketClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *WebSocketClient),
		unregister: make(chan *WebSocketClient),
		logger:     logger,
	}
}

// Run 运行WebSocket中心
func (h *WebSocketHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.logger.Infow("WebSocket client registered", "user_id", client.UserID, "tenant_id", client.TenantID)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.IsClosed = true
				close(client.Send)
				h.logger.Infow("WebSocket client unregistered", "user_id", client.UserID)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			// 修复：broadcast 分支会 delete map + close channel，属于写操作，
			// 必须用 Lock 而非 RLock，否则并发写 map 导致 runtime panic。
			h.mu.Lock()
			for client := range h.clients {
				select {
				case client.Send <- websocketFrame{payload: message}:
				default:
					client.IsClosed = true
					close(client.Send)
					delete(h.clients, client)
				}
			}
			h.mu.Unlock()
		}
	}
}

// RegisterClient 注册客户端
func (h *WebSocketHub) RegisterClient(client *WebSocketClient) {
	h.register <- client
}

// UnregisterClient 注销客户端
func (h *WebSocketHub) UnregisterClient(client *WebSocketClient) {
	h.unregister <- client
}

// SendToUser is best-effort only; durable owners use DeliverToUser.
func (h *WebSocketHub) SendToUser(tenantID, userID int, message WebSocketMessage) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	h.enqueueUser(context.Background(), tenantID, userID, payload, false)
}

// enqueueUser is the only user-target selection path for both kinds of send.
func (h *WebSocketHub) enqueueUser(ctx context.Context, tenantID, userID int, payload []byte, acknowledge bool) (<-chan error, int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var receipts chan error
	if acknowledge {
		receipts = make(chan error, len(h.clients))
	}
	enqueued := 0
	if tenantID <= 0 || userID <= 0 {
		return receipts, enqueued
	}
	for client := range h.clients {
		if client.TenantID != tenantID || client.UserID != userID || client.IsClosed {
			continue
		}
		select {
		case client.Send <- websocketFrame{payload: payload, receipt: receipts, ctx: ctx}:
			enqueued++
		default:
		}
	}
	return receipts, enqueued
}

// DeliverToUser waits for socket write acceptance from at least one connection
// belonging to the exact tenant/user. It does not imply browser consumption.
func (h *WebSocketHub) DeliverToUser(ctx context.Context, tenantID, userID int, message WebSocketMessage) error {
	if ctx == nil || tenantID <= 0 || userID <= 0 {
		return errPushNotAccepted
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(errPushNotAccepted, err)
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return errors.Join(errPushNotAccepted, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	receipts, enqueued := h.enqueueUser(ctx, tenantID, userID, payload, true)
	if enqueued == 0 {
		return errPushNotAccepted
	}
	for i := 0; i < enqueued; i++ {
		select {
		case err := <-receipts:
			if err == nil {
				return nil
			}
		case <-ctx.Done():
			return errors.Join(errPushUnknown, ctx.Err())
		}
	}
	return errPushUnknown
}

// SendToTenant 发送消息给租户所有用户
func (h *WebSocketHub) SendToTenant(tenantID int, message WebSocketMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	msgBytes, err := json.Marshal(message)
	if err != nil {
		h.logger.Errorw("Failed to marshal websocket message", "error", err)
		return
	}

	for client := range h.clients {
		if client.TenantID == tenantID && !client.IsClosed {
			select {
			case client.Send <- websocketFrame{payload: msgBytes}:
			default:
				h.logger.Warnw("Failed to send message to tenant user", "user_id", client.UserID)
			}
		}
	}
}

// BroadcastToAll 广播消息给所有用户
func (h *WebSocketHub) BroadcastToAll(message WebSocketMessage) {
	msgBytes, err := json.Marshal(message)
	if err != nil {
		return
	}
	h.broadcast <- msgBytes
}

// ReadPump 读取客户端消息
func (c *WebSocketClient) ReadPump() {
	defer func() {
		c.Hub.UnregisterClient(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(512)
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.Hub.logger.Errorw("WebSocket error", "error", err, "client_id", c.ID)
			}
			break
		}

		// 处理客户端消息
		var wsMsg WebSocketMessage
		if err := json.Unmarshal(message, &wsMsg); err != nil {
			c.Hub.logger.Warnw("Invalid WebSocket message", "error", err)
			continue
		}

		c.handleMessage(wsMsg)
	}
}

// WritePump 写入消息给客户端
func (c *WebSocketClient) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
		// Serialize closure with all Hub producers and release queued waiters.
		c.Hub.mu.Lock()
		c.IsClosed = true
		delete(c.Hub.clients, c)
		for {
			select {
			case frame, ok := <-c.Send:
				if !ok {
					c.Hub.mu.Unlock()
					return
				}
				frame.finish(errPushUnknown)
			default:
				c.Hub.mu.Unlock()
				return
			}
		}

	}()

	for {
		select {
		case frame, ok := <-c.Send:
			if !ok {
				return
			}
			if frame.ctx != nil && frame.ctx.Err() != nil {
				frame.finish(frame.ctx.Err())
				continue
			}
			if err := c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				frame.finish(err)
				return
			}
			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				frame.finish(err)
				return
			}
			_, writeErr := w.Write(frame.payload)
			closeErr := w.Close()
			err = errors.Join(writeErr, closeErr)
			frame.finish(err)
			if err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage 处理客户端消息
func (c *WebSocketClient) handleMessage(msg WebSocketMessage) {
	switch msg.Type {
	case "ping":
		response := WebSocketMessage{Type: "pong", Payload: nil}
		msgBytes, _ := json.Marshal(response)
		c.Hub.mu.RLock()
		if !c.IsClosed {
			select {
			case c.Send <- websocketFrame{payload: msgBytes}:
			default:
			}
		}
		c.Hub.mu.RUnlock()
	case "subscribe":
		// 处理订阅
		c.Hub.logger.Infow("Client subscribed", "client_id", c.ID)
	case "unsubscribe":
		// 处理取消订阅
		c.Hub.logger.Infow("Client unsubscribed", "client_id", c.ID)
	}
}

// WebSocketService WebSocket服务
type WebSocketService struct {
	hub    *WebSocketHub
	logger *zap.SugaredLogger
}

// NewWebSocketService 创建WebSocket服务
func NewWebSocketService(logger *zap.SugaredLogger) *WebSocketService {
	hub := NewWebSocketHub(logger)
	go hub.Run()

	return &WebSocketService{
		hub:    hub,
		logger: logger,
	}
}

// GetHub 获取WebSocket中心
func (s *WebSocketService) GetHub() *WebSocketHub {
	return s.hub
}

// HandleWebSocket 处理WebSocket连接
func (s *WebSocketService) HandleWebSocket(w http.ResponseWriter, r *http.Request, userID, tenantID int) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Errorw("Failed to upgrade WebSocket", "error", err)
		return
	}

	client := &WebSocketClient{
		ID:       fmt.Sprintf("%d-%d", userID, time.Now().Unix()),
		UserID:   userID,
		TenantID: tenantID,
		Conn:     conn,
		Send:     make(chan websocketFrame, 256),
		Hub:      s.hub,
	}

	s.hub.RegisterClient(client)

	go client.WritePump()
	go client.ReadPump()
}

// NotifyTicketCreated 通知工单创建
func (s *WebSocketService) NotifyTicketCreated(tenantID int, ticket *dto.TicketResponse) {
	msg := WebSocketMessage{
		Type: "ticket_created",
		Payload: map[string]interface{}{
			"ticket": ticket,
		},
	}
	s.hub.SendToTenant(tenantID, msg)
}

// NotifyTicketUpdated 通知工单更新
func (s *WebSocketService) NotifyTicketUpdated(tenantID int, ticket *dto.TicketResponse, changedFields []string) {
	msg := WebSocketMessage{
		Type: "ticket_updated",
		Payload: map[string]interface{}{
			"ticket":         ticket,
			"changed_fields": changedFields,
		},
	}
	s.hub.SendToTenant(tenantID, msg)
}

// NotifyTicketAssigned 通知工单分配
func (s *WebSocketService) NotifyTicketAssigned(tenantID int, ticket *dto.TicketResponse, assigneeID int) {
	msg := WebSocketMessage{
		Type: "ticket_assigned",
		Payload: map[string]interface{}{
			"ticket":      ticket,
			"assignee_id": assigneeID,
		},
	}
	// 发送给创建者
	s.hub.SendToUser(tenantID, ticket.RequesterID, msg)
	// 发送给被分配人
	if assigneeID != ticket.RequesterID {
		s.hub.SendToUser(tenantID, assigneeID, msg)
	}
}

// NotifyTicketCommented 通知工单评论
func (s *WebSocketService) NotifyTicketCommented(tenantID int, ticketID int, comment *dto.TicketCommentResponse) {
	msg := WebSocketMessage{
		Type: "ticket_commented",
		Payload: map[string]interface{}{
			"ticket_id": ticketID,
			"comment":   comment,
		},
	}
	s.hub.SendToTenant(tenantID, msg)
}

// NotifySLABreached 通知SLA违反
func (s *WebSocketService) NotifySLABreached(tenantID int, ticket *dto.TicketResponse, slaName string) {
	msg := WebSocketMessage{
		Type: "sla_breached",
		Payload: map[string]interface{}{
			"ticket":   ticket,
			"sla_name": slaName,
		},
	}
	// 发送给相关人员
	if ticket.AssigneeID > 0 {
		s.hub.SendToUser(tenantID, ticket.AssigneeID, msg)
	}
	s.hub.SendToUser(tenantID, ticket.RequesterID, msg)
}

// NotifyApprovalRequired 通知需要审批
func (s *WebSocketService) NotifyApprovalRequired(tenantID int, userID int, approvalInfo map[string]interface{}) {
	msg := WebSocketMessage{
		Type:    "approval_required",
		Payload: approvalInfo,
	}
	s.hub.SendToUser(tenantID, userID, msg)
}
