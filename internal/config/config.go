package config

import "os"

type Config struct {
	Listen              string
	DataPath            string
	WebDir              string
	APIToken            string
	ShippoAPIToken      string
	ShippoWebhookToken  string
	FirebaseCredentials string
	GmailClientID       string
	GmailClientSecret   string
	GmailPublicURL      string
	EncryptionKey       string
}

func Load() Config {
	return Config{
		Listen:              value("NIMOTSU_LISTEN", ":8080"),
		DataPath:            value("NIMOTSU_DATA_PATH", "./data/nimotsu.db"),
		WebDir:              value("NIMOTSU_WEB_DIR", "./web/dist"),
		APIToken:            os.Getenv("NIMOTSU_API_TOKEN"),
		ShippoAPIToken:      os.Getenv("NIMOTSU_SHIPPO_API_TOKEN"),
		ShippoWebhookToken:  os.Getenv("NIMOTSU_SHIPPO_WEBHOOK_TOKEN"),
		FirebaseCredentials: os.Getenv("NIMOTSU_FIREBASE_CREDENTIALS"),
		GmailClientID:       os.Getenv("NIMOTSU_GMAIL_CLIENT_ID"),
		GmailClientSecret:   os.Getenv("NIMOTSU_GMAIL_CLIENT_SECRET"),
		GmailPublicURL:      os.Getenv("NIMOTSU_PUBLIC_URL"),
		EncryptionKey:       os.Getenv("NIMOTSU_ENCRYPTION_KEY"),
	}
}

func value(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
