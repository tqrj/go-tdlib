package client

import (
	"encoding/json/v2"
	"testing"
)

func TestJSONProtocolRoundTrip(t *testing.T) {
	req := &SendMessageRequest{
		ChatId: 9007199254740993,
		InputMessageContent: &InputMessageText{
			Text: &FormattedText{Text: "中文 <>&"},
		},
	}
	req.SetType(req.GetFunctionName())
	req.SetExtra("request-1")
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Type    string `json:"@type"`
		Extra   string `json:"@extra"`
		ChatID  int64  `json:"chat_id"`
		Content struct {
			Type string `json:"@type"`
			Text struct {
				Type string `json:"@type"`
				Text string `json:"text"`
			} `json:"text"`
		} `json:"input_message_content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Type != "sendMessage" || wire.Extra != "request-1" || wire.ChatID != req.ChatId ||
		wire.Content.Type != "inputMessageText" || wire.Content.Text.Type != "formattedText" || wire.Content.Text.Text != "中文 <>&" {
		t.Fatalf("unexpected request: %s", data)
	}

	update, err := UnmarshalType([]byte(`{"@type":"updateNewMessage","message":{"@type":"message","id":9007199254740993,"content":{"@type":"messageText","text":{"@type":"formattedText","text":"中文","entities":[{"@type":"textEntity","offset":0,"length":2,"type":{"@type":"textEntityTypeBold"}}]},"link_preview":null}}}`))
	if err != nil {
		t.Fatal(err)
	}
	msg := update.(*UpdateNewMessage).Message
	content, ok := msg.Content.(*MessageText)
	if !ok || msg.Id != 9007199254740993 || content.Text.Text != "中文" || content.LinkPreview != nil || len(content.Text.Entities) != 1 {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if _, ok := content.Text.Entities[0].Type.(*TextEntityTypeBold); !ok {
		t.Fatalf("unexpected entity: %+v", content.Text.Entities[0])
	}
}

func TestJSONInt64Wire(t *testing.T) {
	for _, input := range []string{`9223372036854775807`, `"9223372036854775807"`} {
		var value JsonInt64
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil || string(data) != `"9223372036854775807"` {
			t.Fatalf("got %s, %v", data, err)
		}
	}
}
