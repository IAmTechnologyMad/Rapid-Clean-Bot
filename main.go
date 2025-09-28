package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

const (
	botToken = "7107149803:AAEaUMPfRTdoN9KxIEGNInq0kThtIOLxPSA" // ⚠️ Replace with your token
	apiURL   = "https://api.telegram.org/bot" + botToken
)

type UpdateResponse struct {
	OK     bool     `json:"ok"`
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

func main() {
	offset := 0

	for {
		updates, err := getUpdates(offset)
		if err != nil {
			log.Println("getUpdates error:", err)
			time.Sleep(5 * time.Second) // retry quickly
			continue
		}

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			if upd.Message != nil {
				chatID := upd.Message.Chat.ID
				msgID := upd.Message.MessageID
				log.Printf("📩 Got message %d in chat %d", msgID, chatID)

				// Schedule deletion after 3 hours
				go func(cID int64, mID int) {
					time.Sleep(3 * time.Hour)
					if err := deleteMessage(cID, mID); err != nil {
						log.Printf("❌ Delete failed for %d: %v", mID, err)
					} else {
						log.Printf("🗑 Deleted message %d", mID)
					}
				}(chatID, msgID)
			}
		}
	}
}

// Poll Telegram for updates
func getUpdates(offset int) ([]Update, error) {
	resp, err := http.Get(fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30", apiURL, offset))
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

// Delete a message
func deleteMessage(chatID int64, messageID int) error {
	params := url.Values{}
	params.Set("chat_id", fmt.Sprintf("%d", chatID))
	params.Set("message_id", fmt.Sprintf("%d", messageID))

	resp, err := http.PostForm(apiURL+"/deleteMessage", params)
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
