// Package msgraph implements an ITSM connector backed by Microsoft Graph API
// (app-only OAuth2 client-credentials flow) for a shared mailbox: polling
// inbound mail via delta queries and sending replies via sendMail.
//
// Docs:
//   - App-only auth: https://learn.microsoft.com/en-us/graph/auth-v2-service
//   - Delta query on mail: https://learn.microsoft.com/en-us/graph/api/message-delta
//   - Send mail: https://learn.microsoft.com/en-us/graph/api/user-sendmail
package msgraph

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	DefaultAADBaseURL   = "https://login.microsoftonline.com"
	DefaultGraphBaseURL = "https://graph.microsoft.com/v1.0"
)

// Client is an MS Graph HTTP client scoped to a single Azure AD app
// registration (app-only / client_credentials).
type Client struct {
	aadBaseURL   string
	graphBaseURL string
	tenantID     string
	clientID     string
	clientSecret string
	logger       *zap.SugaredLogger
	hc           *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

// NewClient constructs a Graph client. aadBaseURL/graphBaseURL may be empty
// to use the real Microsoft endpoints — non-empty values are for tests
// (httptest.Server URLs).
func NewClient(tenantID, clientID, clientSecret, aadBaseURL, graphBaseURL string) *Client {
	if aadBaseURL == "" {
		aadBaseURL = DefaultAADBaseURL
	}
	if graphBaseURL == "" {
		graphBaseURL = DefaultGraphBaseURL
	}
	return &Client{
		aadBaseURL:   aadBaseURL,
		graphBaseURL: graphBaseURL,
		tenantID:     tenantID,
		clientID:     clientID,
		clientSecret: clientSecret,
		logger:       zap.S().Named("connector.msgraph"),
		hc:           &http.Client{Timeout: 15 * time.Second},
	}
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// Token returns a cached access token, refreshing it if it's missing or
// within 2 minutes of expiry.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.exp) > 2*time.Minute {
		return c.token, nil
	}

	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
		"grant_type":    {"client_credentials"},
	}
	tokenURL := fmt.Sprintf("%s/%s/oauth2/v2.0/token", c.aadBaseURL, c.tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("msgraph: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("msgraph: token request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out tokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("msgraph: decode token response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("msgraph: token error: %s - %s", out.Error, out.ErrorDesc)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("msgraph: empty access token in response (status %d)", resp.StatusCode)
	}

	c.token = out.AccessToken
	c.exp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return c.token, nil
}

// getJSON issues an authenticated GET against an absolute Graph URL.
func (c *Client) getJSON(ctx context.Context, absoluteURL string, out interface{}) error {
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, absoluteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	// 请求 Graph 返回纯文本正文而非 HTML：Outlook 默认发 HTML 邮件，
	// 不加此头时 body.content 会是一整段带 <style>/<meta> 标签的 HTML，
	// 直接存入工单 description 会污染内容。
	req.Header.Set("Prefer", `outlook.body-content-type="text"`)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("msgraph: GET %s: %w", absoluteURL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("msgraph: GET %s: status %d: %s", absoluteURL, resp.StatusCode, string(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("msgraph: decode response from %s: %w", absoluteURL, err)
		}
	}
	return nil
}

// postJSON issues an authenticated POST with a JSON body against a
// Graph-relative path (appended to graphBaseURL).
func (c *Client) postJSON(ctx context.Context, path string, payload interface{}) error {
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("msgraph: encode request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("msgraph: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("msgraph: POST %s: status %d: %s", path, resp.StatusCode, string(raw))
	}
	return nil
}

// Message is a parsed inbound email, ready for ticket creation.
type Message struct {
	ID                string
	ConversationID    string
	InternetMessageID string
	Subject           string
	BodyContentType   string
	BodyContent       string
	FromAddress       string
	ReceivedDateTime  time.Time
	HasAttachments    bool
}

type deltaMessage struct {
	ID                string    `json:"id"`
	ConversationID    string    `json:"conversationId"`
	InternetMessageID string    `json:"internetMessageId"`
	Subject           string    `json:"subject"`
	ReceivedDateTime  time.Time `json:"receivedDateTime"`
	HasAttachments    bool      `json:"hasAttachments"`
	From              struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
}

type deltaResponse struct {
	NextLink  string         `json:"@odata.nextLink"`
	DeltaLink string         `json:"@odata.deltaLink"`
	Value     []deltaMessage `json:"value"`
}

// PollDelta fetches new/changed messages in the mailbox's inbox since the
// given deltaLink (pass "" for the very first call, which returns a full
// snapshot of current unprocessed state per Graph's delta semantics).
// It follows @odata.nextLink pages until it reaches @odata.deltaLink, and
// returns the accumulated messages plus the new deltaLink to persist for
// the next poll.
func (c *Client) PollDelta(ctx context.Context, mailbox, deltaLink string) ([]Message, string, error) {
	link := deltaLink
	if link == "" {
		// $deltatoken=latest tells Graph to skip straight to "you're now
		// caught up" instead of enumerating the mailbox's entire existing
		// history as if it were all brand-new mail (Graph's documented
		// behavior for a delta query with no prior token/link is to return
		// the full current folder contents). Known, accepted tradeoff: since
		// this connector does not persist deltaLink across restarts (see
		// design doc), a message that arrives during the gap between a
		// restart and the coordinator's next poll could be skipped rather
		// than reprocessed. This is intentional, not something to "fix" here.
		link = fmt.Sprintf("%s/users/%s/mailFolders('inbox')/messages/delta?$deltatoken=latest", c.graphBaseURL, url.PathEscape(mailbox))
	}

	var messages []Message
	for {
		var resp deltaResponse
		if err := c.getJSON(ctx, link, &resp); err != nil {
			return nil, "", err
		}
		for _, v := range resp.Value {
			messages = append(messages, Message{
				ID:                v.ID,
				ConversationID:    v.ConversationID,
				InternetMessageID: v.InternetMessageID,
				Subject:           v.Subject,
				BodyContentType:   v.Body.ContentType,
				BodyContent:       v.Body.Content,
				FromAddress:       strings.ToLower(v.From.EmailAddress.Address),
				ReceivedDateTime:  v.ReceivedDateTime,
				HasAttachments:    v.HasAttachments,
			})
		}
		if resp.DeltaLink != "" {
			return messages, resp.DeltaLink, nil
		}
		if resp.NextLink == "" {
			return messages, "", fmt.Errorf("msgraph: delta response missing both nextLink and deltaLink")
		}
		link = resp.NextLink
	}
}

// SendMail sends a plain-text email from the shared mailbox to an arbitrary
// recipient. It carries no conversation threading — use ReplyMessage to reply
// to a specific inbound message within the same conversation thread.
func (c *Client) SendMail(ctx context.Context, mailbox, toAddress, subject, body, deliveryID string) error {
	headers := []map[string]interface{}{{"name": "X-Auto-Submitted", "value": "auto-replied"}}
	if deliveryID != "" {
		headers = append(headers, map[string]interface{}{"name": "X-ITSM-Delivery-ID", "value": deliveryID})
	}
	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": "Text",
				"content":     body,
			},
			"toRecipients": []map[string]interface{}{
				{"emailAddress": map[string]string{"address": toAddress}},
			},
			// Mail sent through this connector is always
			// system/automation-generated (ticket confirmation replies,
			// connector-triggered notifications) — never a human typing a
			// reply. Marking it X-Auto-Submitted lets receiving mail systems
			// (and any out-of-office auto-responders) suppress their own
			// auto-replies back into the shared mailbox, preventing a mail
			// loop. Note: Graph's internetMessageHeaders only accepts custom
			// headers prefixed with "x-", so the RFC 3834 standard header
			// "Auto-Submitted" must be sent as "X-Auto-Submitted".
			"internetMessageHeaders": headers,
		},
		"saveToSentItems": "false",
	}
	path := fmt.Sprintf("/users/%s/sendMail", url.PathEscape(mailbox))
	return c.postJSON(ctx, path, payload)
}

// ReplyMessage replies to a specific inbound message via Graph's reply API,
// which keeps the reply in the same conversation thread. This is the correct
// way to continue a conversation: the message.conversationId field on sendMail
// is read-only and silently ignored by Graph, so sendMail cannot thread a reply.
func (c *Client) ReplyMessage(ctx context.Context, mailbox, messageID, subject, body string) error {
	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": "Text",
				"content":     body,
			},
		},
	}
	path := fmt.Sprintf("/users/%s/messages/%s/reply", url.PathEscape(mailbox), url.PathEscape(messageID))
	return c.postJSON(ctx, path, payload)
}

// Attachment is a mail attachment. Data holds the decoded bytes for small
// attachments (<3MB, Graph returns contentBytes); for larger attachments Data
// is nil and DownloadAttachment must be called to fetch the raw bytes.
type Attachment struct {
	ID          string
	Name        string
	ContentType string
	Size        int
	IsInline    bool
	Data        []byte
}

type attachmentListResponse struct {
	Value []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		ContentType  string `json:"contentType"`
		Size         int    `json:"size"`
		IsInline     bool   `json:"isInline"`
		ContentBytes string `json:"contentBytes"`
	} `json:"value"`
}

// ListAttachments lists a message's attachments. Small attachments include
// their decoded content in Data; large attachments have Data == nil.
func (c *Client) ListAttachments(ctx context.Context, mailbox, messageID string) ([]Attachment, error) {
	path := fmt.Sprintf("/users/%s/messages/%s/attachments", url.PathEscape(mailbox), url.PathEscape(messageID))
	var resp attachmentListResponse
	if err := c.getJSON(ctx, c.graphBaseURL+path, &resp); err != nil {
		return nil, err
	}
	atts := make([]Attachment, 0, len(resp.Value))
	for _, v := range resp.Value {
		var data []byte
		if v.ContentBytes != "" {
			decoded, err := base64.StdEncoding.DecodeString(v.ContentBytes)
			if err != nil {
				return nil, fmt.Errorf("msgraph: decode attachment %q: %w", v.Name, err)
			}
			data = decoded
		}
		atts = append(atts, Attachment{
			ID:          v.ID,
			Name:        v.Name,
			ContentType: v.ContentType,
			Size:        v.Size,
			IsInline:    v.IsInline,
			Data:        data,
		})
	}
	return atts, nil
}

// DownloadAttachment fetches the raw bytes of a large attachment via the
// $value endpoint (returns binary, not JSON).
func (c *Client) DownloadAttachment(ctx context.Context, mailbox, messageID, attachmentID string) ([]byte, error) {
	path := fmt.Sprintf("/users/%s/messages/%s/attachments/%s/$value",
		url.PathEscape(mailbox), url.PathEscape(messageID), url.PathEscape(attachmentID))
	tok, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.graphBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("msgraph: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("msgraph: GET %s: status %d: %s", path, resp.StatusCode, string(raw))
	}
	return io.ReadAll(resp.Body)
}
