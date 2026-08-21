package gmail

import (
	"encoding/base64"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

type messageSummary struct {
	ID string `json:"id"`
}

type gmailMessage struct {
	ID           string      `json:"id"`
	Snippet      string      `json:"snippet"`
	InternalDate string      `json:"internalDate"`
	Payload      messagePart `json:"payload"`
}

type messagePart struct {
	MimeType string          `json:"mimeType"`
	Headers  []messageHeader `json:"headers"`
	Body     messageBody     `json:"body"`
	Parts    []messagePart   `json:"parts"`
}

type messageHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type messageBody struct {
	Data string `json:"data"`
}

func (m gmailMessage) header(name string) string {
	for _, header := range m.Payload.Headers {
		if strings.EqualFold(header.Name, name) {
			return strings.TrimSpace(header.Value)
		}
	}
	return ""
}

func (m gmailMessage) searchableText() string {
	parts := []string{m.header("Subject"), m.Snippet}
	m.Payload.appendText(&parts)
	return strings.Join(parts, "\n")
}

func (m gmailMessage) description() string {
	description := strings.TrimSpace(m.header("Subject"))
	if description == "" {
		description = "Package from " + m.sender()
	}
	if len(description) > 160 {
		description = description[:160]
	}
	return description
}

func (m gmailMessage) sender() string {
	raw := m.header("From")
	address, err := mail.ParseAddress(raw)
	if err == nil {
		if strings.TrimSpace(address.Name) != "" {
			return address.Name
		}
		return address.Address
	}
	if len(raw) > 160 {
		return raw[:160]
	}
	return raw
}

func (m gmailMessage) receivedAt() time.Time {
	milliseconds, err := strconv.ParseInt(m.InternalDate, 10, 64)
	if err != nil {
		return time.Now().UTC().Truncate(time.Second)
	}
	return time.UnixMilli(milliseconds).UTC()
}

func (p messagePart) appendText(destination *[]string) {
	if (p.MimeType == "text/plain" || p.MimeType == "text/html" || p.MimeType == "") && p.Body.Data != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(p.Body.Data)
		if err == nil && len(decoded) <= 1<<20 {
			*destination = append(*destination, string(decoded))
		}
	}
	for _, part := range p.Parts {
		part.appendText(destination)
	}
}
