package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TelegramConfig stores settings for the Telegram Bot integration.
type TelegramConfig struct {
	Enabled            bool   `json:"enabled"`
	BotToken           string `json:"bot_token"`
	ChatID             string `json:"chat_id"`
	WebAppURL          string `json:"webapp_url"`
	NotifyJobStart     bool   `json:"notify_job_start"`
	NotifyJobComplete  bool   `json:"notify_job_complete"`
	NotifyJobFail      bool   `json:"notify_job_fail"`
	NotifyWorkerEvents bool   `json:"notify_worker_events"`
}

// TelegramBot manages Telegram messaging, polling, commands, and alerts.
type TelegramBot struct {
	mu               sync.RWMutex
	config           *TelegramConfig
	server           *Server
	botUser          *tgUser
	httpClient       *http.Client
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	lastUpdate       int64
	lastJobStatus    map[string]string
	lastWorkerStatus map[string]string
}

// Telegram API structures
type tgResponse struct {
	Ok          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username,omitempty"`
}

type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from,omitempty"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text,omitempty"`
	Date      int64   `json:"date"`
}

type tgChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    *tgUser    `json:"from"`
	Message *tgMessage `json:"message,omitempty"`
	Data    string     `json:"data,omitempty"`
}

type tgWebAppInfo struct {
	URL string `json:"url"`
}

type tgInlineKeyboardButton struct {
	Text         string        `json:"text"`
	CallbackData string        `json:"callback_data,omitempty"`
	URL          string        `json:"url,omitempty"`
	WebApp       *tgWebAppInfo `json:"web_app,omitempty"`
}

type tgInlineKeyboardMarkup struct {
	InlineKeyboard [][]tgInlineKeyboardButton `json:"inline_keyboard"`
}

// NewTelegramBot creates a new TelegramBot instance.
func NewTelegramBot(cfg *TelegramConfig, server *Server) (*TelegramBot, error) {
	if cfg == nil {
		cfg = &TelegramConfig{
			Enabled:           false,
			NotifyJobStart:    true,
			NotifyJobComplete: true,
			NotifyJobFail:     true,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := &TelegramBot{
		config: cfg,
		server: server,
		httpClient: &http.Client{
			Timeout: 35 * time.Second,
		},
		ctx:              ctx,
		cancel:           cancel,
		lastJobStatus:    make(map[string]string),
		lastWorkerStatus: make(map[string]string),
	}

	return b, nil
}

// Start validates the token and starts the polling loop if enabled.
func (b *TelegramBot) Start() error {
	b.mu.Lock()
	token := strings.TrimSpace(b.config.BotToken)
	enabled := b.config.Enabled
	b.mu.Unlock()

	if !enabled || token == "" {
		return nil
	}

	// Verify token with getMe
	user, err := b.getMe(token)
	if err != nil {
		return fmt.Errorf("verify telegram bot token: %w", err)
	}

	b.mu.Lock()
	b.botUser = user
	b.mu.Unlock()

	log.Printf("[telegram] Bot authenticated as @%s (ID: %d)", user.Username, user.ID)

	// Configure bot commands
	_ = b.setMyCommands()

	// Configure menu button if WebAppURL is set
	_ = b.setupMenuButton()

	// Start update polling loop
	b.wg.Add(1)
	go b.pollUpdates()

	return nil
}

// Stop terminates the polling loop and waits for goroutines to exit.
func (b *TelegramBot) Stop() {
	b.cancel()
	b.wg.Wait()
}

// UpdateConfig updates the bot's configuration in-memory and reconfigures if token changed.
func (b *TelegramBot) UpdateConfig(cfg *TelegramConfig) error {
	b.mu.Lock()
	oldToken := b.config.BotToken
	b.config = cfg
	b.mu.Unlock()

	if cfg.BotToken != oldToken && cfg.BotToken != "" {
		user, err := b.getMe(cfg.BotToken)
		if err == nil {
			b.mu.Lock()
			b.botUser = user
			b.mu.Unlock()
			_ = b.setMyCommands()
			_ = b.setupMenuButton()
		}
	}
	return nil
}

// GetInfo returns current bot status for the API.
func (b *TelegramBot) GetInfo() map[string]interface{} {
	b.mu.RLock()
	defer b.mu.RUnlock()

	info := map[string]interface{}{
		"enabled":              b.config.Enabled,
		"authorized":           b.botUser != nil,
		"chat_id":              b.config.ChatID,
		"webapp_url":           b.config.WebAppURL,
		"notify_job_start":     b.config.NotifyJobStart,
		"notify_job_complete":  b.config.NotifyJobComplete,
		"notify_job_fail":      b.config.NotifyJobFail,
		"notify_worker_events": b.config.NotifyWorkerEvents,
	}

	if b.botUser != nil {
		info["bot_username"] = b.botUser.Username
		info["bot_name"] = b.botUser.FirstName
		info["bot_id"] = b.botUser.ID
	}

	return info
}

// apiURL returns the Telegram API endpoint URL for a given method.
func (b *TelegramBot) apiURL(method string) string {
	b.mu.RLock()
	token := b.config.BotToken
	b.mu.RUnlock()
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
}

func (b *TelegramBot) getMe(token string) (*tgUser, error) {
	resp, err := b.httpClient.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tgResp tgResponse
	if err := json.Unmarshal(body, &tgResp); err != nil {
		return nil, err
	}
	if !tgResp.Ok {
		return nil, fmt.Errorf("api error: %s", tgResp.Description)
	}

	var user tgUser
	if err := json.Unmarshal(tgResp.Result, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (b *TelegramBot) setMyCommands() error {
	commands := []map[string]string{
		{"command": "start", "description": "Launch dashboard & overview"},
		{"command": "status", "description": "View cluster health & metrics"},
		{"command": "workers", "description": "List connected workers"},
		{"command": "jobs", "description": "List active & recent jobs"},
		{"command": "setchat", "description": "Bind this chat for alerts"},
		{"command": "help", "description": "Show help and command list"},
	}

	payload, _ := json.Marshal(map[string]interface{}{"commands": commands})
	req, _ := http.NewRequestWithContext(b.ctx, http.MethodPost, b.apiURL("setMyCommands"), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (b *TelegramBot) setupMenuButton() error {
	b.mu.RLock()
	webAppURL := strings.TrimSpace(b.config.WebAppURL)
	b.mu.RUnlock()

	var menuButton map[string]interface{}
	if webAppURL != "" && (strings.HasPrefix(webAppURL, "https://") || strings.HasPrefix(webAppURL, "http://")) {
		if strings.HasPrefix(webAppURL, "https://") {
			menuButton = map[string]interface{}{
				"type": "web_app",
				"text": "CNC UI",
				"web_app": map[string]string{
					"url": webAppURL,
				},
			}
		} else {
			menuButton = map[string]interface{}{
				"type": "default",
			}
		}
	} else {
		menuButton = map[string]interface{}{
			"type": "default",
		}
	}

	payload, _ := json.Marshal(map[string]interface{}{"menu_button": menuButton})
	req, _ := http.NewRequestWithContext(b.ctx, http.MethodPost, b.apiURL("setChatMenuButton"), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// pollUpdates runs the long-polling loop for receiving messages and button presses.
func (b *TelegramBot) pollUpdates() {
	defer b.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.fetchAndProcessUpdates()
		}
	}
}

func (b *TelegramBot) fetchAndProcessUpdates() {
	b.mu.RLock()
	token := b.config.BotToken
	offset := b.lastUpdate + 1
	b.mu.RUnlock()

	if token == "" {
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=20", token, offset)
	req, err := http.NewRequestWithContext(b.ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var tgResp tgResponse
	if err := json.Unmarshal(body, &tgResp); err != nil || !tgResp.Ok {
		return
	}

	var updates []tgUpdate
	if err := json.Unmarshal(tgResp.Result, &updates); err != nil {
		return
	}

	for _, update := range updates {
		if update.UpdateID > b.lastUpdate {
			b.lastUpdate = update.UpdateID
		}

		if update.Message != nil {
			b.handleIncomingMessage(update.Message)
		} else if update.CallbackQuery != nil {
			b.handleCallbackQuery(update.CallbackQuery)
		}
	}
}

func (b *TelegramBot) handleIncomingMessage(msg *tgMessage) {
	if msg.Text == "" {
		return
	}

	text := strings.TrimSpace(msg.Text)
	chatID := strconv.FormatInt(msg.Chat.ID, 10)

	// If no chat ID has been saved yet, auto-register this chat
	b.mu.Lock()
	if b.config.ChatID == "" {
		b.config.ChatID = chatID
		log.Printf("[telegram] Auto-registered alert Chat ID: %s", chatID)
		b.saveConfigAsync()
	}
	b.mu.Unlock()

	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	if atIdx := strings.Index(cmd, "@"); atIdx != -1 {
		cmd = cmd[:atIdx]
	}

	switch cmd {
	case "/start":
		b.sendStartResponse(chatID)
	case "/status", "/stats":
		b.sendStatusResponse(chatID)
	case "/workers":
		b.sendWorkersResponse(chatID)
	case "/jobs":
		b.sendJobsResponse(chatID)
	case "/cancel":
		if len(parts) > 1 {
			b.handleCancelCommand(chatID, parts[1])
		} else {
			_ = b.SendMessage(chatID, "⚠️ Usage: `/cancel <job_id>`", nil)
		}
	case "/setchat":
		b.mu.Lock()
		b.config.ChatID = chatID
		b.saveConfigAsync()
		b.mu.Unlock()
		_ = b.SendMessage(chatID, fmt.Sprintf("✅ This chat (`%s`) is now configured for CNC cluster alerts.", chatID), nil)
	case "/help":
		b.sendHelpResponse(chatID)
	default:
		b.sendStartResponse(chatID)
	}
}

func (b *TelegramBot) handleCallbackQuery(cb *tgCallbackQuery) {
	b.answerCallbackQuery(cb.ID)

	chatID := strconv.FormatInt(cb.From.ID, 10)
	if cb.Message != nil {
		chatID = strconv.FormatInt(cb.Message.Chat.ID, 10)
	}

	switch cb.Data {
	case "cmd:status":
		b.sendStatusResponse(chatID)
	case "cmd:workers":
		b.sendWorkersResponse(chatID)
	case "cmd:jobs":
		b.sendJobsResponse(chatID)
	case "cmd:help":
		b.sendHelpResponse(chatID)
	default:
		if strings.HasPrefix(cb.Data, "cancel:") {
			jobID := strings.TrimPrefix(cb.Data, "cancel:")
			b.handleCancelCommand(chatID, jobID)
		}
	}
}

func (b *TelegramBot) answerCallbackQuery(callbackID string) {
	payload, _ := json.Marshal(map[string]string{
		"callback_query_id": callbackID,
	})
	req, _ := http.NewRequestWithContext(b.ctx, http.MethodPost, b.apiURL("answerCallbackQuery"), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (b *TelegramBot) buildInlineKeyboard() *tgInlineKeyboardMarkup {
	b.mu.RLock()
	webAppURL := strings.TrimSpace(b.config.WebAppURL)
	b.mu.RUnlock()

	var rows [][]tgInlineKeyboardButton

	// Row 1: Web App / Dashboard button
	if webAppURL != "" {
		if strings.HasPrefix(webAppURL, "https://") {
			rows = append(rows, []tgInlineKeyboardButton{
				{
					Text:   "🖥️ Open Web Dashboard",
					WebApp: &tgWebAppInfo{URL: webAppURL},
				},
			})
		} else {
			rows = append(rows, []tgInlineKeyboardButton{
				{
					Text: "🖥️ Open Web Dashboard",
					URL:  webAppURL,
				},
			})
		}
	}

	// Row 2: Cluster controls
	rows = append(rows, []tgInlineKeyboardButton{
		{Text: "📊 Cluster Status", CallbackData: "cmd:status"},
		{Text: "👷 Workers", CallbackData: "cmd:workers"},
	})

	// Row 3: Jobs & Help
	rows = append(rows, []tgInlineKeyboardButton{
		{Text: "📋 Active Jobs", CallbackData: "cmd:jobs"},
		{Text: "❓ Help", CallbackData: "cmd:help"},
	})

	return &tgInlineKeyboardMarkup{InlineKeyboard: rows}
}

func (b *TelegramBot) sendStartResponse(chatID string) {
	fullStats := b.server.GetFullStats()

	workersOnline, _ := fullStats["workers_online"].(int)
	workersTotal, _ := fullStats["workers_total"].(int)
	jobsRunning, _ := fullStats["jobs_running"].(int)
	jobsTotal, _ := fullStats["jobs_total"].(int)
	tasksCompleted, _ := fullStats["tasks_completed"].(int)
	tasksFailed, _ := fullStats["tasks_failed"].(int)

	text := fmt.Sprintf(
		"🚀 *CNC Distributed Cluster Dashboard*\n\n"+
			"Cluster is online and monitored.\n"+
			"• *Workers Online:* `%d / %d`\n"+
			"• *Running Jobs:* `%d` (Total: `%d`)\n"+
			"• *Tasks:* `%d` completed, `%d` failed\n\n"+
			"Use the buttons below to monitor or manage the cluster:",
		workersOnline, workersTotal,
		jobsRunning, jobsTotal,
		tasksCompleted, tasksFailed,
	)

	kb := b.buildInlineKeyboard()
	_ = b.SendMessage(chatID, text, kb)
}

func (b *TelegramBot) sendStatusResponse(chatID string) {
	fullStats := b.server.GetFullStats()

	workersOnline, _ := fullStats["workers_online"].(int)
	workersTotal, _ := fullStats["workers_total"].(int)
	jobsRunning, _ := fullStats["jobs_running"].(int)
	jobsTotal, _ := fullStats["jobs_total"].(int)
	tasksRunning, _ := fullStats["tasks_running"].(int)
	tasksPending, _ := fullStats["tasks_pending"].(int)
	tasksCompleted, _ := fullStats["tasks_completed"].(int)
	tasksFailed, _ := fullStats["tasks_failed"].(int)

	gdriveEnabled, _ := fullStats["gdrive_enabled"].(bool)
	gdriveStatus := "Disabled"
	if gdriveEnabled {
		uploaded, _ := fullStats["gdrive_uploaded"].(int64)
		pending, _ := fullStats["gdrive_pending"].(int)
		gdriveStatus = fmt.Sprintf("Active (%d uploaded, %d pending)", uploaded, pending)
	}

	text := fmt.Sprintf(
		"📊 *Cluster Status Report*\n\n"+
			"🟢 *System:* Online\n"+
			"👷 *Workers:* `%d online` / `%d total`\n"+
			"💼 *Jobs:* `%d running` / `%d total`\n"+
			"⚡ *Tasks:*\n"+
			"   • Running: `%d`\n"+
			"   • Pending: `%d`\n"+
			"   • Completed: `%d`\n"+
			"   • Failed: `%d`\n"+
			"☁️ *Google Drive Sync:* `%s`\n"+
			"🕒 *Timestamp:* `%s`",
		workersOnline, workersTotal,
		jobsRunning, jobsTotal,
		tasksRunning,
		tasksPending,
		tasksCompleted,
		tasksFailed,
		gdriveStatus,
		time.Now().Format("2006-01-02 15:04:05 MST"),
	)

	kb := b.buildInlineKeyboard()
	_ = b.SendMessage(chatID, text, kb)
}

func (b *TelegramBot) sendWorkersResponse(chatID string) {
	b.server.mu.RLock()
	var workersList []*Worker
	for _, w := range b.server.workers {
		workerCopy := *w
		workersList = append(workersList, &workerCopy)
	}
	b.server.mu.RUnlock()

	if len(workersList) == 0 {
		_ = b.SendMessage(chatID, "⚠️ *Workers:* No workers connected to the cluster.", b.buildInlineKeyboard())
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👷 *Connected Workers (%d)*\n\n", len(workersList)))

	for _, w := range workersList {
		statusIcon := "🟢"
		switch w.Status {
		case WorkerStatusOffline:
			statusIcon = "🔴"
		case WorkerStatusBusy:
			statusIcon = "🟡"
		}

		sb.WriteString(fmt.Sprintf("%s *%s*\n", statusIcon, w.ID))
		sb.WriteString(fmt.Sprintf("   • Address: `%s`\n", w.Address))
		sb.WriteString(fmt.Sprintf("   • Load: `%d / %d tasks`\n", w.CurrentLoad, w.MaxTasks))
		sb.WriteString(fmt.Sprintf("   • Status: `%s`\n\n", w.Status))
	}

	_ = b.SendMessage(chatID, sb.String(), b.buildInlineKeyboard())
}

func (b *TelegramBot) sendJobsResponse(chatID string) {
	b.server.mu.RLock()
	var jobsList []*Job
	for _, j := range b.server.jobs {
		jobCopy := *j
		jobsList = append(jobsList, &jobCopy)
	}
	b.server.mu.RUnlock()

	if len(jobsList) == 0 {
		_ = b.SendMessage(chatID, "📋 *Jobs:* No jobs submitted yet.", b.buildInlineKeyboard())
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 *Recent Jobs (%d)*\n\n", len(jobsList)))

	start := 0
	if len(jobsList) > 5 {
		start = len(jobsList) - 5
	}

	for i := len(jobsList) - 1; i >= start; i-- {
		j := jobsList[i]
		statusIcon := "⏳"
		switch j.Status {
		case "running":
			statusIcon = "🔄"
		case "completed":
			statusIcon = "✅"
		case "failed":
			statusIcon = "❌"
		case "cancelled":
			statusIcon = "🚫"
		}

		pct := 0
		if j.TotalTasks > 0 {
			pct = (j.Completed * 100) / j.TotalTasks
		}

		barWidth := 10
		filled := (pct * barWidth) / 100
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

		sb.WriteString(fmt.Sprintf("%s *Job #%s* — %s\n", statusIcon, j.ID, j.Name))
		sb.WriteString(fmt.Sprintf("   • Command: `%s`\n", j.Command))
		sb.WriteString(fmt.Sprintf("   • Progress: `[%s] %d%%` (%d/%d)\n", bar, pct, j.Completed, j.TotalTasks))
		sb.WriteString(fmt.Sprintf("   • Status: `%s`\n\n", j.Status))
	}

	_ = b.SendMessage(chatID, sb.String(), b.buildInlineKeyboard())
}

func (b *TelegramBot) handleCancelCommand(chatID string, jobID string) {
	jobID = strings.TrimSpace(jobID)
	err := b.server.CancelJob(jobID)
	if err != nil {
		_ = b.SendMessage(chatID, fmt.Sprintf("❌ Failed to cancel Job `#%s`: %v", jobID, err), nil)
		return
	}

	_ = b.SendMessage(chatID, fmt.Sprintf("🚫 Successfully cancelled Job `#%s`.", jobID), nil)
}

func (b *TelegramBot) sendHelpResponse(chatID string) {
	text := "📖 *CNC Telegram Commands*\n\n" +
		"• `/start` — Show overview and quick access buttons\n" +
		"• `/status` — Detailed cluster status\n" +
		"• `/workers` — List online and registered workers\n" +
		"• `/jobs` — List jobs and execution progress\n" +
		"• `/cancel <id>` — Cancel a running job\n" +
		"• `/setchat` — Bind this chat for automated cluster alerts\n" +
		"• `/help` — Display this command reference"

	_ = b.SendMessage(chatID, text, b.buildInlineKeyboard())
}

// SendMessage sends a markdown-formatted message to a specific chat ID.
func (b *TelegramBot) SendMessage(chatID string, text string, replyMarkup *tgInlineKeyboardMarkup) error {
	b.mu.RLock()
	token := b.config.BotToken
	b.mu.RUnlock()

	if token == "" || chatID == "" {
		return fmt.Errorf("bot token or chat ID is empty")
	}

	payloadMap := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if replyMarkup != nil {
		payloadMap["reply_markup"] = replyMarkup
	}

	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, b.apiURL("sendMessage"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram api error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// SendTestMessage sends a test notification to verify integration.
func (b *TelegramBot) SendTestMessage(targetChatID string) error {
	if targetChatID == "" {
		b.mu.RLock()
		targetChatID = b.config.ChatID
		b.mu.RUnlock()
	}

	if targetChatID == "" {
		return fmt.Errorf("no chat_id provided and none registered yet. Start the bot with /start in Telegram first")
	}

	text := "🧪 *CNC Cluster Alert Test*\n\n" +
		"✅ Telegram Bot integration is working correctly!\n" +
		fmt.Sprintf("• Connected to CNC Server at `%s`\n", time.Now().Format("2006-01-02 15:04:05 MST")) +
		"• Real-time job and cluster alerts are enabled."

	return b.SendMessage(targetChatID, text, b.buildInlineKeyboard())
}

// HandleJobEvent tracks job status changes and fires alerts without spamming.
func (b *TelegramBot) HandleJobEvent(job *Job) {
	if job == nil || job.ID == "" {
		return
	}

	b.mu.Lock()
	prevStatus := b.lastJobStatus[job.ID]
	b.lastJobStatus[job.ID] = job.Status
	b.mu.Unlock()

	// If status has not changed, do not re-notify
	if prevStatus == job.Status {
		return
	}

	if job.Status == "running" && prevStatus != "running" {
		b.NotifyJobStart(job)
	} else if job.Status == "completed" {
		b.NotifyJobComplete(job)
	} else if job.Status == "failed" {
		b.NotifyJobFail(job, "Job terminated with failed tasks")
	}
}

// HandleWorkerEvent tracks worker status changes and fires alerts.
func (b *TelegramBot) HandleWorkerEvent(worker *Worker) {
	if worker == nil || worker.ID == "" {
		return
	}

	b.mu.Lock()
	prevStatus := b.lastWorkerStatus[worker.ID]
	b.lastWorkerStatus[worker.ID] = string(worker.Status)
	b.mu.Unlock()

	if prevStatus != "" && prevStatus != string(worker.Status) {
		b.NotifyWorkerStatus(worker.ID, string(worker.Status), worker.Address)
	}
}

// NotifyJobStart sends an alert when a job begins execution.
func (b *TelegramBot) NotifyJobStart(job *Job) {
	b.mu.RLock()
	enabled := b.config.Enabled && b.config.NotifyJobStart
	chatID := b.config.ChatID
	b.mu.RUnlock()

	if !enabled || chatID == "" || job == nil {
		return
	}

	text := fmt.Sprintf(
		"🚀 *Job Started* `#%s`\n\n"+
			"• *Name:* %s\n"+
			"• *Command:* `%s`\n"+
			"• *Tasks:* `%d`\n"+
			"• *Time:* `%s`",
		job.ID, job.Name, job.Command, job.TotalTasks,
		time.Now().Format("15:04:05"),
	)

	go func() {
		_ = b.SendMessage(chatID, text, nil)
	}()
}

// NotifyJobComplete sends an alert when a job finishes.
func (b *TelegramBot) NotifyJobComplete(job *Job) {
	b.mu.RLock()
	enabled := b.config.Enabled && b.config.NotifyJobComplete
	chatID := b.config.ChatID
	b.mu.RUnlock()

	if !enabled || chatID == "" || job == nil {
		return
	}

	text := fmt.Sprintf(
		"✅ *Job Completed* `#%s`\n\n"+
			"• *Name:* %s\n"+
			"• *Completed Tasks:* `%d / %d`\n"+
			"• *Failed Tasks:* `%d`\n"+
			"• *Finished At:* `%s`",
		job.ID, job.Name, job.Completed, job.TotalTasks, job.Failed,
		time.Now().Format("15:04:05"),
	)

	go func() {
		_ = b.SendMessage(chatID, text, nil)
	}()
}

// NotifyJobFail sends an alert when a job fails.
func (b *TelegramBot) NotifyJobFail(job *Job, reason string) {
	b.mu.RLock()
	enabled := b.config.Enabled && b.config.NotifyJobFail
	chatID := b.config.ChatID
	b.mu.RUnlock()

	if !enabled || chatID == "" || job == nil {
		return
	}

	text := fmt.Sprintf(
		"❌ *Job Failed* `#%s`\n\n"+
			"• *Name:* %s\n"+
			"• *Reason:* %s\n"+
			"• *Tasks Done:* `%d / %d`\n"+
			"• *Time:* `%s`",
		job.ID, job.Name, reason, job.Completed, job.TotalTasks,
		time.Now().Format("15:04:05"),
	)

	go func() {
		_ = b.SendMessage(chatID, text, nil)
	}()
}

// NotifyWorkerStatus sends an alert when a worker's status changes.
func (b *TelegramBot) NotifyWorkerStatus(workerID string, status string, address string) {
	b.mu.RLock()
	enabled := b.config.Enabled && b.config.NotifyWorkerEvents
	chatID := b.config.ChatID
	b.mu.RUnlock()

	if !enabled || chatID == "" {
		return
	}

	icon := "🔌"
	switch status {
	case "online":
		icon = "🟢"
	case "offline":
		icon = "🔴"
	}

	text := fmt.Sprintf(
		"%s *Worker Status Changed*\n\n"+
			"• *Worker:* `%s`\n"+
			"• *Status:* `%s`\n"+
			"• *Address:* `%s`\n"+
			"• *Time:* `%s`",
		icon, workerID, status, address,
		time.Now().Format("15:04:05"),
	)

	go func() {
		_ = b.SendMessage(chatID, text, nil)
	}()
}

func (b *TelegramBot) saveConfigAsync() {
	go func() {
		cfg, err := LoadServerConfig("server_config.json")
		if err != nil {
			return
		}
		b.mu.RLock()
		cfg.Telegram = b.config
		b.mu.RUnlock()
		_ = SaveServerConfig("server_config.json", cfg)
	}()
}
