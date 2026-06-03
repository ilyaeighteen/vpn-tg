package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"vpn-tg/internal/xui"
)

type Config struct {
	TelegramBotToken string
	InitialAdminIDs  []int64
	AdminsFile       string
	XUI              xui.Config
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		TelegramBotToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		AdminsFile:       getenv("ADMINS_FILE", "admins.json"),
	}

	if cfg.TelegramBotToken == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}

	ids, err := parseInt64List(os.Getenv("INITIAL_ADMIN_IDS"))
	if err != nil {
		return Config{}, fmt.Errorf("INITIAL_ADMIN_IDS: %w", err)
	}
	cfg.InitialAdminIDs = ids

	xuiCfg, err := loadXUI()
	if err != nil {
		return Config{}, err
	}
	cfg.XUI = xuiCfg

	return cfg, nil
}

func loadXUI() (xui.Config, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("XUI_BASE_URL")), "/")
	apiToken := strings.TrimSpace(os.Getenv("XUI_API_TOKEN"))

	if baseURL == "" {
		return xui.Config{}, errors.New("XUI_BASE_URL is required")
	}
	if apiToken == "" {
		return xui.Config{}, errors.New("XUI_API_TOKEN is required")
	}

	inboundID, err := getInt("XUI_INBOUND_ID", 0)
	if err != nil {
		return xui.Config{}, err
	}
	if inboundID <= 0 {
		return xui.Config{}, errors.New("XUI_INBOUND_ID must be greater than 0")
	}

	limitIP, err := getInt("XUI_CLIENT_LIMIT_IP", 0)
	if err != nil {
		return xui.Config{}, err
	}
	totalGB, err := getInt64("XUI_CLIENT_TOTAL_GB", 0)
	if err != nil {
		return xui.Config{}, err
	}
	expireDays, err := getInt("XUI_CLIENT_EXPIRE_DAYS", 0)
	if err != nil {
		return xui.Config{}, err
	}
	enable, err := getBool("XUI_CLIENT_ENABLE", true)
	if err != nil {
		return xui.Config{}, err
	}

	timeout, err := getDuration("XUI_HTTP_TIMEOUT", 15*time.Second)
	if err != nil {
		return xui.Config{}, err
	}

	return xui.Config{
		BaseURL:     baseURL,
		APIToken:    apiToken,
		InboundID:   inboundID,
		HTTPTimeout: timeout,
		DefaultClient: xui.ClientDefaults{
			Flow:       strings.TrimSpace(os.Getenv("XUI_CLIENT_FLOW")),
			LimitIP:    limitIP,
			TotalGB:    totalGB * 1024 * 1024 * 1024,
			ExpireDays: expireDays,
			Enable:     enable,
		},
	}, nil
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func parseInt64List(raw string) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	result := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		parsed, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid id %q: %w", part, err)
		}
		result = append(result, parsed)
	}
	return result, nil
}

func getInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func getInt64(key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func getBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 15s: %w", key, err)
	}
	return parsed, nil
}
