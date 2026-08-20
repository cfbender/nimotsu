package config

import "os"

type Config struct {
	Listen              string
	DataPath            string
	WebDir              string
	APIToken            string
	SeventeenTrackKey   string
	FirebaseCredentials string
}

func Load() Config {
	return Config{
		Listen:              value("NIMOTSU_LISTEN", ":8080"),
		DataPath:            value("NIMOTSU_DATA_PATH", "./data/nimotsu.db"),
		WebDir:              value("NIMOTSU_WEB_DIR", "./web/dist"),
		APIToken:            os.Getenv("NIMOTSU_API_TOKEN"),
		SeventeenTrackKey:   os.Getenv("NIMOTSU_17TRACK_KEY"),
		FirebaseCredentials: os.Getenv("NIMOTSU_FIREBASE_CREDENTIALS"),
	}
}

func value(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
