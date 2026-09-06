// Package feishu provides the Feishu/Lark connector.
package feishu

import "itsm-backend/connector"

// Public callbacks are handled by FeishuController.Webhook.
var (
	_ connector.Connector = (*Feishu)(nil)
	_ connector.Receiver  = (*Feishu)(nil)
)
