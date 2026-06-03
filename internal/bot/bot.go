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
	qrcode "github.com/skip2/go-qrcode"

	"vpn-tg/internal/admins"
	"vpn-tg/internal/users"
	"vpn-tg/internal/xui"
)

const (
	callbackCreateClient = "create_client"
	callbackClients      = "clients"
	callbackClientLinks  = "client_links"
	callbackClientPrefix = "client:"
	callbackDeleteClient = "delete_client"
	callbackAdmins       = "admins"
	callbackAddAdmin     = "add_admin"
	callbackBack         = "back"
	callbackCancel       = "cancel"
	callbackRemovePrefix = "remove_admin:"

	stateNone state = iota
	stateAwaitClientEmail
	stateAwaitClientLinksEmail
	stateAwaitDeleteClientEmail
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
	ListClients(ctx context.Context, inboundID int) ([]xui.PanelClient, error)
	FindClientByID(ctx context.Context, inboundID int, clientID string) (xui.PanelClient, error)
	DeleteClientByEmail(ctx context.Context, inboundID int, email string) error
	GetClientLinks(ctx context.Context, inboundID int, email string) (xui.ClientLinksResult, error)
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
	case stateAwaitClientLinksEmail:
		b.sendClientLinks(chatID, userID, message.Text)
	case stateAwaitDeleteClientEmail:
		b.deleteClient(chatID, userID, message.Text)
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
	case data == callbackClients:
		b.setState(userID, stateNone)
		b.showClients(query.Message)
	case data == callbackClientLinks:
		b.setState(userID, stateAwaitClientLinksEmail)
		b.editOrSend(query.Message, "Введите email клиента для вывода ссылок и QR", cancelKeyboard())
	case strings.HasPrefix(data, callbackClientPrefix):
		b.setState(userID, stateNone)
		b.sendClientLinksByID(query.Message.Chat.ID, userID, strings.TrimPrefix(data, callbackClientPrefix))
	case data == callbackDeleteClient:
		b.setState(userID, stateAwaitDeleteClientEmail)
		b.editOrSend(query.Message, "Введите email клиента, которого нужно удалить из inbound #"+strconv.Itoa(b.inboundID), cancelKeyboard())
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

func (b *Bot) showClients(message *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clients, err := b.xui.ListClients(ctx, b.inboundID)
	if err != nil {
		log.Printf("list clients failed: %v", err)
		b.editOrSend(message, "Не удалось получить клиентов: "+err.Error(), mainKeyboard())
		return
	}

	if len(clients) == 0 {
		b.editOrSend(message, "В inbound #"+strconv.Itoa(b.inboundID)+" клиентов нет", clientsKeyboard(nil))
		return
	}

	lines := []string{"Клиенты inbound #" + strconv.Itoa(b.inboundID) + ":", "Нажмите на клиента, чтобы получить ссылки и QR."}
	for i, client := range clients {
		lines = append(lines, strconv.Itoa(i+1)+". "+client.Email)
	}

	b.editOrSend(message, strings.Join(lines, "\n"), clientsKeyboard(clients))
}

func (b *Bot) sendClientLinks(chatID int64, userID int64, email string) {
	email = strings.TrimSpace(email)
	if email == "" {
		b.send(chatID, "Email не должен быть пустым", cancelKeyboard())
		return
	}

	b.setState(userID, stateNone)
	b.send(chatID, "Получаю ссылки клиента...", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := b.xui.GetClientLinks(ctx, b.inboundID, email)
	if err != nil {
		log.Printf("get client links failed: %v", err)
		b.sendMainMenu(chatID, "Не удалось получить ссылки клиента: "+err.Error())
		return
	}
	if len(result.Links) == 0 {
		b.send(chatID, "Для клиента "+result.Email+" ссылки не найдены", clientsKeyboard(nil))
		return
	}

	b.sendLongText(chatID, formatClientLinks(result), clientsKeyboard(nil))
	if result.SubscriptionURL != "" {
		b.sendQRCodeWithCaption(chatID, safeFilename(result.Email)+"-subscription.png", "QR подписки для "+result.Email, result.SubscriptionURL)
	}
	for i, link := range result.Links {
		b.sendQRCode(chatID, result.Email, i+1, link)
	}
}

func (b *Bot) sendClientLinksByID(chatID int64, userID int64, clientID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := b.xui.FindClientByID(ctx, b.inboundID, clientID)
	if err != nil {
		log.Printf("find client failed: %v", err)
		b.sendMainMenu(chatID, "Не удалось найти клиента: "+err.Error())
		return
	}

	b.sendClientLinks(chatID, userID, client.Email)
}

func (b *Bot) deleteClient(chatID int64, userID int64, email string) {
	email = strings.TrimSpace(email)
	if email == "" {
		b.send(chatID, "Email не должен быть пустым", cancelKeyboard())
		return
	}

	b.setState(userID, stateNone)
	b.send(chatID, "Удаляю клиента из 3x-ui...", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.xui.DeleteClientByEmail(ctx, b.inboundID, email); err != nil {
		log.Printf("delete client failed: %v", err)
		b.sendMainMenu(chatID, "Не удалось удалить клиента: "+err.Error())
		return
	}

	b.send(chatID, "Клиент удален: "+email, mainKeyboard())
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

func (b *Bot) sendLongText(chatID int64, text string, markup any) {
	const maxMessageLen = 3900

	for len(text) > maxMessageLen {
		cut := strings.LastIndex(text[:maxMessageLen], "\n")
		if cut <= 0 {
			cut = maxMessageLen
		}
		b.send(chatID, text[:cut], nil)
		text = strings.TrimSpace(text[cut:])
	}
	b.send(chatID, text, markup)
}

func (b *Bot) sendQRCode(chatID int64, email string, index int, link string) {
	b.sendQRCodeWithCaption(chatID, safeFilename(email)+"-"+strconv.Itoa(index)+".png", "QR "+strconv.Itoa(index)+" для "+email, link)
}

func (b *Bot) sendQRCodeWithCaption(chatID int64, filename string, caption string, link string) {
	png, err := qrcode.Encode(link, qrcode.Medium, 512)
	if err != nil {
		log.Printf("generate qr failed: %v", err)
		b.send(chatID, "Не удалось создать QR: "+err.Error(), nil)
		return
	}

	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{
		Name:  filename,
		Bytes: png,
	})
	photo.Caption = caption
	if _, err := b.api.Send(photo); err != nil {
		log.Printf("send qr failed: %v", err)
		b.send(chatID, "Не удалось отправить QR: "+err.Error(), nil)
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

func formatClientLinks(result xui.ClientLinksResult) string {
	lines := []string{"Ссылки клиента " + result.Email + ":"}
	if result.SubscriptionURL != "" {
		lines = append(lines, "", "URL подписки:", result.SubscriptionURL)
	}
	for i, link := range result.Links {
		lines = append(lines, "", strconv.Itoa(i+1)+". "+link)
	}
	return strings.Join(lines, "\n")
}

func safeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "client"
	}

	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "client"
	}
	return builder.String()
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
			tgbotapi.NewInlineKeyboardButtonData("Клиенты", callbackClients),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Админы", callbackAdmins),
		),
	)
}

func clientsKeyboard(clients []xui.PanelClient) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(clients)+3)
	for _, client := range clients {
		if client.ID == "" {
			continue
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(client.Email, callbackClientPrefix+client.ID),
		))
	}

	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Ссылки и QR", callbackClientLinks),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Удалить клиента", callbackDeleteClient),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Назад", callbackBack),
		),
	)
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
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
