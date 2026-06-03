package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"vpn-tg/internal/admins"
	"vpn-tg/internal/users"
	"vpn-tg/internal/xui"
)

const (
	callbackCreateClient = "create_client"
	callbackAdmins       = "admins"
	callbackAddAdmin     = "add_admin"
	callbackBack         = "back"
	callbackCancel       = "cancel"
	callbackRemovePrefix = "remove_admin:"

	stateNone state = iota
	stateAwaitClientEmail
	stateAwaitAdminID
)

type state int

type AdminStore interface {
	IsAdmin(id int64) bool
	List() []int64
	Add(id int64) error
	Remove(id int64) error
}

type UserStore interface {
	Save(user users.User) error
	FindByUsername(username string) (users.User, error)
}

type XUIClient interface {
	AddClient(ctx context.Context, inboundID int, email string) (xui.AddClientResult, error)
}

type Bot struct {
	api       *tgbotapi.BotAPI
	admins    AdminStore
	users     UserStore
	xui       XUIClient
	inboundID int

	statesMu sync.Mutex
	states   map[int64]state
}

func New(token string, adminStore AdminStore, userStore UserStore, xuiClient XUIClient, inboundID int) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	return &Bot{
		api:       api,
		admins:    adminStore,
		users:     userStore,
		xui:       xuiClient,
		inboundID: inboundID,
		states:    make(map[int64]state),
	}, nil
}

func (b *Bot) Run() error {
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 60

	updates := b.api.GetUpdatesChan(updateConfig)
	for update := range updates {
		if update.Message != nil {
			b.handleMessage(update.Message)
			continue
		}
		if update.CallbackQuery != nil {
			b.handleCallback(update.CallbackQuery)
		}
	}
	return nil
}

func (b *Bot) handleMessage(message *tgbotapi.Message) {
	userID := message.From.ID
	chatID := message.Chat.ID

	b.rememberUser(message.From)

	if !b.admins.IsAdmin(userID) {
		b.send(chatID, "Доступ запрещен. Ваш Telegram ID: "+strconv.FormatInt(userID, 10), nil)
		return
	}

	if message.IsCommand() {
		b.setState(userID, stateNone)
		switch message.Command() {
		case "start", "menu":
			b.sendMainMenu(chatID, "Панель управления")
		default:
			b.sendMainMenu(chatID, "Неизвестная команда")
		}
		return
	}

	switch b.getState(userID) {
	case stateAwaitClientEmail:
		b.createClient(chatID, userID, message.Text)
	case stateAwaitAdminID:
		b.addAdmin(chatID, userID, message.Text)
	default:
		b.sendMainMenu(chatID, "Выберите действие")
	}
}

func (b *Bot) handleCallback(query *tgbotapi.CallbackQuery) {
	userID := query.From.ID

	b.rememberUser(query.From)
	b.answerCallback(query.ID, "")

	if !b.admins.IsAdmin(userID) {
		b.editOrSend(query.Message, "Доступ запрещен.", nil)
		return
	}

	data := query.Data
	switch {
	case data == callbackCreateClient:
		b.setState(userID, stateAwaitClientEmail)
		b.editOrSend(query.Message, "Введите email клиента для inbound #"+strconv.Itoa(b.inboundID), cancelKeyboard())
	case data == callbackAdmins:
		b.setState(userID, stateNone)
		b.editOrSend(query.Message, b.adminsText(), adminsKeyboard(b.admins.List()))
	case data == callbackAddAdmin:
		b.setState(userID, stateAwaitAdminID)
		b.editOrSend(query.Message, "Введите Telegram ID или @username нового админа", cancelKeyboard())
	case data == callbackBack:
		b.setState(userID, stateNone)
		b.editOrSend(query.Message, "Панель управления", mainKeyboard())
	case data == callbackCancel:
		b.setState(userID, stateNone)
		b.editOrSend(query.Message, "Действие отменено", mainKeyboard())
	case strings.HasPrefix(data, callbackRemovePrefix):
		b.removeAdmin(query.Message, userID, strings.TrimPrefix(data, callbackRemovePrefix))
	default:
		b.editOrSend(query.Message, "Неизвестное действие", mainKeyboard())
	}
}

func (b *Bot) createClient(chatID int64, userID int64, email string) {
	email = strings.TrimSpace(email)
	if email == "" {
		b.send(chatID, "Email не должен быть пустым", cancelKeyboard())
		return
	}

	b.setState(userID, stateNone)
	b.send(chatID, "Создаю клиента в 3x-ui...", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := b.xui.AddClient(ctx, b.inboundID, email)
	if err != nil {
		log.Printf("create client failed: %v", err)
		b.sendMainMenu(chatID, "Не удалось создать клиента: "+err.Error())
		return
	}

	text := fmt.Sprintf("Клиент создан\n\nEmail: %s\nUUID: %s\nInbound: #%d", result.Email, result.UUID, b.inboundID)
	b.send(chatID, text, mainKeyboard())
}

func (b *Bot) addAdmin(chatID int64, userID int64, rawID string) {
	id, label, err := b.resolveAdminInput(rawID)
	if err != nil {
		b.send(chatID, "Введите Telegram ID или @username пользователя, который уже писал боту", cancelKeyboard())
		return
	}

	b.setState(userID, stateNone)
	if err := b.admins.Add(id); err != nil {
		b.sendMainMenu(chatID, "Не удалось добавить админа: "+err.Error())
		return
	}

	b.send(chatID, "Админ добавлен: "+label, mainKeyboard())
}

func (b *Bot) removeAdmin(message *tgbotapi.Message, actorID int64, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		b.editOrSend(message, "Некорректный ID админа", adminsKeyboard(b.admins.List()))
		return
	}

	if err := b.admins.Remove(id); err != nil {
		if errors.Is(err, admins.ErrLastAdmin) {
			b.editOrSend(message, "Нельзя удалить последнего админа", adminsKeyboard(b.admins.List()))
			return
		}
		b.editOrSend(message, "Не удалось удалить админа: "+err.Error(), adminsKeyboard(b.admins.List()))
		return
	}

	text := "Админ удален: " + strconv.FormatInt(id, 10)
	if id == actorID {
		text += "\n\nВы удалили себя из админов."
	}
	b.editOrSend(message, text, adminsKeyboard(b.admins.List()))
}

func (b *Bot) sendMainMenu(chatID int64, text string) {
	b.send(chatID, text, mainKeyboard())
}

func (b *Bot) send(chatID int64, text string, markup any) {
	msg := tgbotapi.NewMessage(chatID, text)
	if markup != nil {
		msg.ReplyMarkup = markup
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send message failed: %v", err)
	}
}

func (b *Bot) editOrSend(message *tgbotapi.Message, text string, markup any) {
	edit := tgbotapi.NewEditMessageText(message.Chat.ID, message.MessageID, text)
	if markup != nil {
		if inline, ok := markup.(tgbotapi.InlineKeyboardMarkup); ok {
			edit.ReplyMarkup = &inline
		}
	}
	if _, err := b.api.Send(edit); err != nil {
		b.send(message.Chat.ID, text, markup)
	}
}

func (b *Bot) answerCallback(callbackID string, text string) {
	callback := tgbotapi.NewCallback(callbackID, text)
	if _, err := b.api.Request(callback); err != nil {
		log.Printf("answer callback failed: %v", err)
	}
}

func (b *Bot) rememberUser(user *tgbotapi.User) {
	if user == nil {
		return
	}

	if err := b.users.Save(users.User{
		ID:        user.ID,
		Username:  user.UserName,
		FirstName: user.FirstName,
		LastName:  user.LastName,
	}); err != nil {
		log.Printf("save telegram user failed: %v", err)
	}
}

func (b *Bot) resolveAdminInput(input string) (int64, string, error) {
	input = strings.TrimSpace(input)
	id, err := strconv.ParseInt(input, 10, 64)
	if err == nil && id > 0 {
		return id, strconv.FormatInt(id, 10), nil
	}

	user, err := b.users.FindByUsername(input)
	if err != nil {
		return 0, "", err
	}

	label := strconv.FormatInt(user.ID, 10)
	if user.Username != "" {
		label += " (@" + user.Username + ")"
	}
	return user.ID, label, nil
}

func (b *Bot) adminsText() string {
	ids := b.admins.List()
	if len(ids) == 0 {
		return "Админов пока нет. Добавьте первого через INITIAL_ADMIN_IDS."
	}

	lines := []string{"Админы:"}
	for _, id := range ids {
		lines = append(lines, "- "+strconv.FormatInt(id, 10))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) setState(userID int64, s state) {
	b.statesMu.Lock()
	defer b.statesMu.Unlock()

	if s == stateNone {
		delete(b.states, userID)
		return
	}
	b.states[userID] = s
}

func (b *Bot) getState(userID int64) state {
	b.statesMu.Lock()
	defer b.statesMu.Unlock()
	return b.states[userID]
}

func mainKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Создать клиента", callbackCreateClient),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Админы", callbackAdmins),
		),
	)
}

func adminsKeyboard(ids []int64) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Добавить админа", callbackAddAdmin),
		),
	}

	for _, id := range ids {
		label := "Удалить " + strconv.FormatInt(id, 10)
		data := callbackRemovePrefix + strconv.FormatInt(id, 10)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, data)))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Назад", callbackBack)))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func cancelKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Отмена", callbackCancel),
		),
	)
}
