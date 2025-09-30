package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// --- CONFIGURATION ---
const (
	botToken    = "7876416156:AAG3cXPdF44mYuH0s5-ldebx7GKjbLV3WHc" // ⚠️ Replace with your bot token
	groupChatID = "-4985438208"                                    // ⚠️ Replace with your group chat ID
	renderURL   = "https://rapid-clean-bot.onrender.com"           // ⚠️ Replace with your Render URL
	deleteAfter = 3 * time.Minute                                    // Time after which messages are deleted
)

// --- STRUCTS ---
type UpdateResponse struct {
	Ok          bool     `json:"ok"`
	Result      []Update `json:"result"`
	Description string   `json:"description,omitempty"`
}

type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID int    `json:"message_id"`
	Chat      *Chat  `json:"chat"`
	From      *User  `json:"from,omitempty"`
	Date      int64  `json:"date"`
	Text      string `json:"text,omitempty"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username,omitempty"`
}

type DeleteResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

// Message tracker to prevent duplicate processing
type MessageTracker struct {
	mu       sync.Mutex
	messages map[string]time.Time
}

func NewMessageTracker() *MessageTracker {
	return &MessageTracker{
		messages: make(map[string]time.Time),
	}
}

func (mt *MessageTracker) Add(chatID int64, messageID int) bool {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	
	key := fmt.Sprintf("%d_%d", chatID, messageID)
	if _, exists := mt.messages[key]; exists {
		return false
	}
	mt.messages[key] = time.Now()
	
	// Clean old entries (older than deleteAfter + 1 hour)
	mt.cleanOldEntries()
	return true
}

func (mt *MessageTracker) cleanOldEntries() {
	cutoff := time.Now().Add(-(deleteAfter + time.Hour))
	for key, timestamp := range mt.messages {
		if timestamp.Before(cutoff) {
			delete(mt.messages, key)
		}
	}
}

var tracker = NewMessageTracker()

// --- MAIN ---
func main() {
	log.Println("🚀 Clean Bot starting...")
	log.Printf("📍 Monitoring chat ID: %s", groupChatID)
	log.Printf("⏱️ Delete messages after: %v", deleteAfter)

	// Verify bot token
	if err := verifyBot(); err != nil {
		log.Fatalf("❌ Failed to verify bot: %v", err)
	}

	// Start HTTP server for Render keep-alive
	go startHTTPServer()

	// Start self-ping keep-alive
	go startKeepAlive(renderURL)

	// Start polling Telegram with long polling
	offset := 0
	failureCount := 0
	
	for {
		updates, err := getUpdates(offset)
		if err != nil {
			failureCount++
			log.Printf("❌ getUpdates error (attempt %d): %v", failureCount, err)
			
			// Exponential backoff
			sleepTime := time.Duration(min(failureCount*2, 30)) * time.Second
			time.Sleep(sleepTime)
			continue
		}
		
		failureCount = 0 // Reset on success

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			
			if upd.Message != nil {
				chatID := upd.Message.Chat.ID
				msgID := upd.Message.MessageID
				
				// Convert groupChatID string to int64 for comparison
				var targetChatID int64
				fmt.Sscanf(groupChatID, "%d", &targetChatID)
				
				// Only process messages from the target group
				if chatID != targetChatID {
					log.Printf("⚠️ Ignoring message from chat %d (not target group)", chatID)
					continue
				}
				
				// Check if we've already scheduled this message
				if !tracker.Add(chatID, msgID) {
					log.Printf("⚠️ Message %d already scheduled for deletion", msgID)
					continue
				}
				
				// Log message details
				username := "Unknown"
				if upd.Message.From != nil {
					username = upd.Message.From.FirstName
					if upd.Message.From.Username != "" {
						username = "@" + upd.Message.From.Username
					}
				}
				
				log.Printf("📩 New message %d from %s in chat %s", msgID, username, upd.Message.Chat.Title)
				
				// Schedule deletion
				go scheduleDelete(chatID, msgID, deleteAfter)
			}
		}
		
		// Small delay to prevent hammering the API
		time.Sleep(100 * time.Millisecond)
	}
}

// --- VERIFY BOT ---
func verifyBot() error {
	resp, err := http.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", botToken))
	if err != nil {
		return fmt.Errorf("network error: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode error: %v", err)
	}

	if ok, exists := result["ok"].(bool); !exists || !ok {
		return fmt.Errorf("invalid bot token or API error: %v", result["description"])
	}

	if botInfo, exists := result["result"].(map[string]interface{}); exists {
		log.Printf("✅ Bot verified: %s (@%s)", 
			botInfo["first_name"], 
			botInfo["username"])
	}
	
	return nil
}

// --- SCHEDULE DELETE ---
func scheduleDelete(chatID int64, messageID int, delay time.Duration) {
	log.Printf("⏳ Message %d scheduled for deletion in %v", messageID, delay)
	
	time.Sleep(delay)
	
	retries := 3
	for i := 0; i < retries; i++ {
		err := deleteMessage(chatID, messageID)
		if err == nil {
			log.Printf("✅ Successfully deleted message %d", messageID)
			return
		}
		
		// Check if it's a permanent error
		if strings.Contains(err.Error(), "message to delete not found") {
			log.Printf("⚠️ Message %d already deleted or not found", messageID)
			return
		}
		
		if strings.Contains(err.Error(), "message can't be deleted") {
			log.Printf("⚠️ Message %d can't be deleted (too old or no permission)", messageID)
			return
		}
		
		log.Printf("❌ Failed to delete message %d (attempt %d/%d): %v", 
			messageID, i+1, retries, err)
		
		if i < retries-1 {
			time.Sleep(time.Duration(i+1) * 5 * time.Second)
		}
	}
}

// --- GET UPDATES ---
func getUpdates(offset int) ([]Update, error) {
	// Use long polling with 30 second timeout
	resp, err := http.Get(fmt.Sprintf(
		"https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=[\"message\"]", 
		botToken, offset))
	if err != nil {
		return nil, fmt.Errorf("network error: %v", err)
	}
	defer resp.Body.Close()

	var ur UpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&ur); err != nil {
		return nil, fmt.Errorf("decode error: %v", err)
	}
	
	if !ur.Ok {
		return nil, fmt.Errorf("telegram API error: %s", ur.Description)
	}
	
	return ur.Result, nil
}

// --- DELETE MESSAGE ---
func deleteMessage(chatID int64, messageID int) error {
	params := url.Values{}
	params.Set("chat_id", fmt.Sprintf("%d", chatID))
	params.Set("message_id", fmt.Sprintf("%d", messageID))

	resp, err := http.PostForm(
		fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", botToken), 
		params)
	if err != nil {
		return fmt.Errorf("network error: %v", err)
	}
	defer resp.Body.Close()

	var result DeleteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode error: %v", err)
	}

	if !result.Ok {
		return fmt.Errorf("API error (code %d): %s", result.ErrorCode, result.Description)
	}
	
	return nil
}

// --- HTTP SERVER FOR RENDER KEEP-ALIVE ---
func startHTTPServer() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "pong")
		log.Println("🏓 Received ping")
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		status := map[string]interface{}{
			"status": "healthy",
			"uptime": time.Since(startTime).String(),
			"bot_token_configured": botToken != "",
			"group_chat_configured": groupChatID != "",
		}
		json.NewEncoder(w).Encode(status)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `
			<html>
			<head><title>Clean Bot Status</title></head>
			<body>
				<h1>🤖 Clean Bot is running!</h1>
				<p>Uptime: %s</p>
				<p>Monitoring chat: %s</p>
				<p>Delete after: %v</p>
			</body>
			</html>`,
			time.Since(startTime), groupChatID, deleteAfter)
	})

	log.Printf("🌐 HTTP server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("❌ HTTP server error: %v", err)
	}
}

// --- KEEP-ALIVE PING ---
func startKeepAlive(url string) {
	ticker := time.NewTicker(8 * time.Minute)
	client := &http.Client{Timeout: 30 * time.Second}
	log.Println("⏰ Keep-alive ping started...")
	
	for {
		select {
		case <-ticker.C:
			resp, err := client.Get(url + "/ping")
			if err != nil {
				log.Printf("⚠️ Keep-alive ping failed: %v", err)
			} else {
				resp.Body.Close()
				log.Printf("✅ Keep-alive ping successful")
			}
		}
	}
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var startTime = time.Now()
