package main

import (
	"log"

	"vpn-tg/internal/admins"
	"vpn-tg/internal/bot"
	"vpn-tg/internal/config"
	"vpn-tg/internal/xui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := admins.NewStore(cfg.AdminsFile, cfg.InitialAdminIDs)
	if err != nil {
		log.Fatalf("init admin store: %v", err)
	}

	xuiClient := xui.NewClient(cfg.XUI)

	app, err := bot.New(cfg.TelegramBotToken, store, xuiClient, cfg.XUI.InboundID)
	if err != nil {
		log.Fatalf("init bot: %v", err)
	}

	log.Println("bot started")
	if err := app.Run(); err != nil {
		log.Fatalf("run bot: %v", err)
	}
}
