package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Minimal Telegram Bot API client (long polling). Only the methods the tracker
// needs are implemented.

const apiBase = "https://api.telegram.org/bot"

type Client struct {
	token string
	http  *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		token: token,
		// Long polling holds the request open ~50s; give it headroom.
		http: &http.Client{Timeout: 70 * time.Second},
	}
}

// ---- update / message types ----

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text"`
	Photo     []PhotoSize `json:"photo"`
	Caption   string      `json:"caption"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int    `json:"file_size"`
}

// ---- reply markup ----

type WebAppInfo struct {
	URL string `json:"url"`
}

type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type KeyboardButton struct {
	Text   string      `json:"text"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

type ReplyKeyboardMarkup struct {
	Keyboard       [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard bool               `json:"resize_keyboard,omitempty"`
	IsPersistent   bool               `json:"is_persistent,omitempty"`
}

// ---- core call ----

func (c *Client) call(ctx context.Context, method string, params map[string]any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		apiBase+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s: decode: %w", method, err)
	}
	if !env.OK {
		return fmt.Errorf("%s: telegram error: %s", method, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// GetMe verifies the token and returns the bot user.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", map[string]any{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	// Use a GET with query params; long-poll timeout handled server-side.
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeoutSec))
	q.Set("allowed_updates", `["message","callback_query"]`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		apiBase+c.token+"/getUpdates?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		OK          bool     `json:"ok"`
		Description string   `json:"description"`
		Result      []Update `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.OK {
		return nil, fmt.Errorf("getUpdates: %s", env.Description)
	}
	return env.Result, nil
}

// send is the core sendMessage call; replyTo>0 makes it a reply, and it returns
// the new message's id.
func (c *Client) send(ctx context.Context, chatID int64, text string, markup any, replyTo int64) (int64, error) {
	p := map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML"}
	if markup != nil {
		p["reply_markup"] = markup
	}
	if replyTo != 0 {
		p["reply_parameters"] = map[string]any{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	var msg Message
	err := c.call(ctx, "sendMessage", p, &msg)
	return msg.MessageID, err
}

// SendMessage sends text with an optional reply markup (any of the markup types).
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, markup any) error {
	_, err := c.send(ctx, chatID, text, markup, 0)
	return err
}

// SendMessageID sends text and returns the new message's id (for later replies).
func (c *Client) SendMessageID(ctx context.Context, chatID int64, text string, markup any) (int64, error) {
	return c.send(ctx, chatID, text, markup, 0)
}

// SendReply replies to replyTo in chatID.
func (c *Client) SendReply(ctx context.Context, chatID, replyTo int64, text string, markup any) error {
	_, err := c.send(ctx, chatID, text, markup, replyTo)
	return err
}

// SendPhoto sends a photo by file_id with optional caption and markup.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, fileID, caption string, markup any) error {
	p := map[string]any{"chat_id": chatID, "photo": fileID}
	if caption != "" {
		p["caption"] = caption
		p["parse_mode"] = "HTML"
	}
	if markup != nil {
		p["reply_markup"] = markup
	}
	return c.call(ctx, "sendPhoto", p, nil)
}

// AnswerCallbackQuery acknowledges a callback button press.
func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", p, nil)
}

// DownloadFile fetches a file's bytes by file_id (getFile + download) and
// returns the bytes plus the file extension (e.g. ".jpg").
func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, string, error) {
	var gf struct {
		FilePath string `json:"file_path"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &gf); err != nil {
		return nil, "", err
	}
	if gf.FilePath == "" {
		return nil, "", fmt.Errorf("getFile: empty file_path")
	}
	url := "https://api.telegram.org/file/bot" + c.token + "/" + gf.FilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	ext := strings.ToLower(path.Ext(gf.FilePath))
	if ext == "" {
		ext = ".jpg"
	}
	return data, ext, nil
}

// BotCommand is one entry in the bot's "/" command menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetCommands publishes the bot's command menu (shown when typing "/"). scope is
// nil for the default scope, or e.g. {"type":"all_group_chats"} for groups.
func (c *Client) SetCommands(ctx context.Context, cmds []BotCommand, scope map[string]any) error {
	p := map[string]any{"commands": cmds}
	if scope != nil {
		p["scope"] = scope
	}
	return c.call(ctx, "setMyCommands", p, nil)
}

// SetDefaultMenuButton points the default chat menu button at a Web App URL.
func (c *Client) SetDefaultMenuButton(ctx context.Context, text, webAppURL string) error {
	return c.call(ctx, "setChatMenuButton", map[string]any{
		"menu_button": map[string]any{
			"type":    "web_app",
			"text":    text,
			"web_app": map[string]any{"url": webAppURL},
		},
	}, nil)
}
