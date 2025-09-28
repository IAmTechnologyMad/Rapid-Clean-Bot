package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

// --- CONFIGURATION ---
const (
	botToken   = "7876416156:AAG3cXPdF44mYuH0s5-ldebx7GKjbLV3WHc"        // ⚠️ Replace with your bot token
	groupChat  = "-4985438208"           // ⚠️ Replace with your group chat ID
	renderURL  = "https://your-bot.onrender.com" // ⚠️ Replace with your Render URL
	deleteAfter = 3 * time.Hour          // Time after which messages are deleted
)

// --- STRUCTS ---
type UpdateResponse struct {
	Ok     bool     `json:"ok"`
	Result []Update `json:"result"`
}

type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID int   `json:"message_id"`
	Chat      *Chat `json:"chat"`
}

type Chat struct {
	ID int64 `json:"id"`
}

// --- MAIN ---
func main() {
	log.Println("🚀 Clean Bot starting...")

	// Start HTTP server for Render keep-alive
	go startHTTPServer()

	// Start self-ping keep-alive
	go startKeepAlive(renderURL)

	// Start polling Telegram
	offset := 0
	for {
		updates, err := getUpdates(offset)
		if err != nil {
			log.Printf("❌ getUpdates error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			if upd.Message != nil {
				chatID := upd.Message.Chat.ID
				msgID := upd.Message.MessageID
				log.Printf("📩 New message %d received in chat %d", msgID, chatID)

				// Schedule deletion after 3 hours
				go func(cID int64, mID int) {
					log.Printf("⏳ Scheduling deletion for message %d in %v", mID, deleteAfter)
					time.Sleep(deleteAfter)
					if err := deleteMessage(cID, mID); err != nil {
						log.Printf("❌ Failed to delete message %d: %v", mID, err)
					} else {
						log.Printf("🗑 Deleted message %d", mID)
					}
				}(chatID, msgID)
			}
		}
	}
}

// --- GET UPDATES ---
func getUpdates(offset int) ([]Update, error) {
	resp, err := http.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", botToken, offset))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ur UpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&ur); err != nil {
		return nil, err
	}
	return ur.Result, nil
}

// --- DELETE MESSAGE ---
func deleteMessage(chatID int64, messageID int) error {
	params := url.Values{}
	params.Set("chat_id", fmt.Sprintf("%d", chatID))
	params.Set("message_id", fmt.Sprintf("%d", messageID))

	resp, err := http.PostForm(fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", botToken), params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if ok, exists := result["ok"].(bool); !exists || !ok {
		return fmt.Errorf("telegram API error: %v", result)
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
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Clean Bot is running!"))
	})

	log.Printf("🌐 HTTP server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("❌ HTTP server error: %v", err)
	}
}

// --- KEEP-ALIVE PING ---
func startKeepAlive(url string) {
	go func() {
		ticker := time.NewTicker(8 * time.Minute)
		client := &http.Client{Timeout: 30 * time.Second}
		log.Println("⏰ Keep-alive ping started...")
		for {
			resp, err := client.Get(url + "/ping")
			if err != nil {
				log.Printf("⚠️ Keep-alive ping failed: %v", err)
			} else {
				resp.Body.Close()
				log.Printf("✅ Keep-alive ping successful")
			}
			<-ticker.C
		}
	}()
}
