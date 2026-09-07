package main

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxPlayers      = 10
	minInitialCards = 3
	maxInitialCards = 20
	deckTotal       = 119
)

type Card struct {
	ID    string `json:"id"`
	Color string `json:"color"`
	Value string `json:"value"`
}

type Player struct {
	ID        string
	Nickname  string
	IP        string
	Connected bool
	ClientID  string
	RoomID    string
	Hand      []Card
}

type Client struct {
	id       string
	conn     *websocket.Conn
	send     chan []byte
	playerID string
	server   *Server
}

type Game struct {
	Deck          []Card
	Discard       []Card
	Top           Card
	ActiveColor   string
	Direction     int
	TurnIdx       int
	StackMode     string // none, plus2, plus4
	StackAmount   int
	DrawnThisTurn bool
}

type Room struct {
	ID           string
	Phase        string // lobby, playing, finished
	CreatorID    string
	PasswordHash string
	InitialCards int
	PlayerIDs    []string
	Game         *Game
	WinnerID     string
	CreatedAt    time.Time
}

type Server struct {
	mu      sync.Mutex
	clients map[string]*Client
	players map[string]*Player
	rooms   map[string]*Room
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func NewServer() *Server {
	return &Server{
		clients: make(map[string]*Client),
		players: make(map[string]*Player),
		rooms:   make(map[string]*Room),
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

func hashPassword(p string) string {
	if p == "" {
		return ""
	}
	h := sha256.Sum256([]byte(p))
	return hex.EncodeToString(h[:])
}

func getIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt(v interface{}, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return def
}

func errMsg(code, message string) map[string]interface{} {
	return map[string]interface{}{
		"type":    "error",
		"code":    code,
		"message": message,
	}
}

func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}

func isValidColor(c string) bool {
	return c == "red" || c == "yellow" || c == "green" || c == "blue"
}

func isNumberValue(v string) bool {
	_, err := strconv.Atoi(v)
	return err == nil
}

func NewDeck() []Card {
	deck := []Card{}
	colors := []string{"red", "yellow", "green", "blue"}

	add := func(color, value string) {
		deck = append(deck, Card{
			ID:    color + "_" + value + "_" + newID(),
			Color: color,
			Value: value,
		})
	}

	for _, color := range colors {
		add(color, "0")
		for n := 1; n <= 9; n++ {
			add(color, strconv.Itoa(n))
			add(color, strconv.Itoa(n))
		}
		for i := 0; i < 2; i++ {
			add(color, "reverse")
			add(color, "skip")
			add(color, "shoot")
			add(color, "draw2")
		}
	}

	for i := 0; i < 4; i++ {
		add("wild", "wild_color")
		add("wild", "wild_draw4")
	}
	for i := 0; i < 3; i++ {
		add("wild", "wild_exchange")
	}

	return deck
}

func (g *Game) currentPlayerID(room *Room) string {
	if len(room.PlayerIDs) == 0 {
		return ""
	}
	if g.TurnIdx < 0 || g.TurnIdx >= len(room.PlayerIDs) {
		return ""
	}
	return room.PlayerIDs[g.TurnIdx]
}

func (g *Game) advance(room *Room, steps int) {
	n := len(room.PlayerIDs)
	if n == 0 {
		return
	}
	for i := 0; i < steps; i++ {
		g.TurnIdx = (g.TurnIdx + g.Direction + n) % n
	}
}

func (s *Server) send(c *Client, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		// Drop if client buffer is full.
	}
}

func (s *Server) sendToPlayerLocked(playerID string, v interface{}) {
	p := s.players[playerID]
	if p == nil || p.ClientID == "" {
		return
	}
	c := s.clients[p.ClientID]
	if c == nil {
		return
	}
	s.send(c, v)
}

func (s *Server) broadcastHandsLocked(room *Room) {
	if room == nil {
		return
	}
	for _, pid := range room.PlayerIDs {
		p := s.players[pid]
		if p == nil {
			continue
		}
		s.sendToPlayerLocked(pid, handMsg(p))
	}
}

func handMsg(p *Player) map[string]interface{} {
	cards := p.Hand
	if cards == nil {
		cards = []Card{}
	}
	return map[string]interface{}{
		"type":  "hand",
		"cards": cards,
	}
}

func (s *Server) challengeListLocked() []map[string]interface{} {
	rooms := []*Room{}
	for _, r := range s.rooms {
		if r.Phase == "lobby" {
			rooms = append(rooms, r)
		}
	}

	sort.Slice(rooms, func(i, j int) bool {
		return rooms[i].CreatedAt.After(rooms[j].CreatedAt)
	})

	list := []map[string]interface{}{}
	for _, r := range rooms {
		creator := s.players[r.CreatorID]
		nick := ""
		if creator != nil {
			nick = creator.Nickname
		}

		list = append(list, map[string]interface{}{
			"id":              r.ID,
			"creatorNickname": nick,
			"playerCount":     len(r.PlayerIDs),
			"maxPlayers":      maxPlayers,
			"initialCards":    r.InitialCards,
			"hasPassword":     r.PasswordHash != "",
		})
	}
	return list
}

func (s *Server) broadcastChallengesLocked() {
	msg := map[string]interface{}{
		"type":       "challenges",
		"challenges": s.challengeListLocked(),
	}
	for _, c := range s.clients {
		s.send(c, msg)
	}
}

func (s *Server) roomPlayersLocked(room *Room) []map[string]interface{} {
	players := []map[string]interface{}{}
	for _, pid := range room.PlayerIDs {
		p := s.players[pid]
		if p == nil {
			continue
		}
		players = append(players, map[string]interface{}{
			"id":        p.ID,
			"nickname":  p.Nickname,
			"connected": p.Connected,
			"handCount": len(p.Hand),
			"isCreator": p.ID == room.CreatorID,
		})
	}
	return players
}

func (s *Server) roomUpdateLocked(room *Room) map[string]interface{} {
	return map[string]interface{}{
		"type": "room_update",
		"room": map[string]interface{}{
			"id":           room.ID,
			"phase":        room.Phase,
			"creatorId":    room.CreatorID,
			"initialCards": room.InitialCards,
			"hasPassword":  room.PasswordHash != "",
			"players":      s.roomPlayersLocked(room),
		},
	}
}

func (s *Server) publicGameStateLocked(room *Room) map[string]interface{} {
	msg := map[string]interface{}{
		"type":    "game_state",
		"roomId":  room.ID,
		"phase":   room.Phase,
		"players": s.roomPlayersLocked(room),
	}

	if room.Game == nil {
		return msg
	}

	g := room.Game
	msg["topCard"] = g.Top
	msg["activeColor"] = g.ActiveColor
	msg["direction"] = g.Direction
	msg["turnPlayerId"] = g.currentPlayerID(room)
	msg["stackMode"] = g.StackMode
	msg["stackAmount"] = g.StackAmount
	msg["drawnThisTurn"] = g.DrawnThisTurn
	msg["winnerId"] = room.WinnerID

	return msg
}

func (s *Server) broadcastRoomStateLocked(room *Room) {
	if room == nil {
		return
	}

	if room.Phase == "lobby" {
		msg := s.roomUpdateLocked(room)
		for _, pid := range room.PlayerIDs {
			s.sendToPlayerLocked(pid, msg)
		}
		return
	}

	msg := s.publicGameStateLocked(room)
	for _, pid := range room.PlayerIDs {
		s.sendToPlayerLocked(pid, msg)
	}
}

func (s *Server) sendGameOverLocked(room *Room) {
	if room == nil {
		return
	}
	winnerNick := ""
	if p := s.players[room.WinnerID]; p != nil {
		winnerNick = p.Nickname
	}
	msg := map[string]interface{}{
		"type":           "game_over",
		"winnerId":       room.WinnerID,
		"winnerNickname": winnerNick,
	}
	for _, pid := range room.PlayerIDs {
		s.sendToPlayerLocked(pid, msg)
	}
}

func (s *Server) endGameLocked(room *Room, winnerID string) {
	if room == nil {
		return
	}
	room.Phase = "finished"
	room.WinnerID = winnerID
	s.broadcastRoomStateLocked(room)
	s.broadcastHandsLocked(room)
	s.sendGameOverLocked(room)
}

func (s *Server) sendRoomSnapshotLocked(c *Client) {
	p := s.players[c.playerID]
	if p == nil || p.RoomID == "" {
		return
	}
	room := s.rooms[p.RoomID]
	if room == nil {
		p.RoomID = ""
		return
	}

	if room.Phase == "lobby" {
		s.send(c, s.roomUpdateLocked(room))
		return
	}

	s.send(c, s.publicGameStateLocked(room))
	s.send(c, handMsg(p))
}

func (s *Server) removePlayerFromRoomLocked(p *Player) {
	if p == nil || p.RoomID == "" {
		return
	}
	room := s.rooms[p.RoomID]
	if room == nil {
		p.RoomID = ""
		return
	}

	idx := indexOf(room.PlayerIDs, p.ID)
	if idx == -1 {
		p.RoomID = ""
		return
	}

	room.PlayerIDs = append(room.PlayerIDs[:idx], room.PlayerIDs[idx+1:]...)
	p.RoomID = ""
	p.Hand = nil

	if len(room.PlayerIDs) == 0 {
		delete(s.rooms, room.ID)
		return
	}

	if room.Phase == "lobby" {
		if room.CreatorID == p.ID {
			room.CreatorID = room.PlayerIDs[0]
		}
		s.broadcastRoomStateLocked(room)
		s.broadcastChallengesLocked()
		return
	}

	if room.Phase == "playing" && room.Game != nil {
		if room.Game.TurnIdx > idx {
			room.Game.TurnIdx--
		}

		if len(room.PlayerIDs) == 1 {
			room.Phase = "finished"
			room.WinnerID = room.PlayerIDs[0]
			s.broadcastRoomStateLocked(room)
			s.sendGameOverLocked(room)
			return
		}

		if room.Game.TurnIdx >= len(room.PlayerIDs) {
			room.Game.TurnIdx = 0
		}
		room.Game.DrawnThisTurn = false
	}

	s.broadcastRoomStateLocked(room)
}

func (s *Server) reshuffleLocked(room *Room) {
	g := room.Game
	if g == nil || len(g.Discard) == 0 {
		return
	}
	g.Deck = append(g.Deck, g.Discard...)
	g.Discard = []Card{}
	rand.Shuffle(len(g.Deck), func(i, j int) {
		g.Deck[i], g.Deck[j] = g.Deck[j], g.Deck[i]
	})
}

func (s *Server) drawCardsLocked(room *Room, playerID string, count int) {
	if room == nil || room.Game == nil {
		return
	}
	p := s.players[playerID]
	if p == nil {
		return
	}

	for i := 0; i < count; i++ {
		if len(room.Game.Deck) == 0 {
			s.reshuffleLocked(room)
		}
		if len(room.Game.Deck) == 0 {
			break
		}
		card := room.Game.Deck[0]
		room.Game.Deck = room.Game.Deck[1:]
		p.Hand = append(p.Hand, card)
	}
}

func canPlayCard(g *Game, card Card) bool {
	if g.StackMode == "plus2" {
		if card.Color != "wild" && card.Value == "draw2" {
			return true
		}
		return card.Value == "wild_draw4"
	}

	if g.StackMode == "plus4" {
		return card.Value == "wild_draw4"
	}

	// Normal or colored state.
	if card.Color == "wild" {
		return true
	}
	if card.Color == g.ActiveColor {
		return true
	}
	if g.Top.Color != "wild" && card.Value == g.Top.Value {
		return true
	}
	return false
}

func (s *Server) moveTopLocked(room *Room, card Card) {
	g := room.Game
	if g == nil {
		return
	}
	if g.Top.ID != "" {
		g.Discard = append(g.Discard, g.Top)
	}
	g.Top = card
}

func (s *Server) applyCardLocked(room *Room, player *Player, card Card, targetID, chosenColor string) {
	g := room.Game
	if g == nil {
		return
	}

	g.DrawnThisTurn = false

	switch card.Value {
	case "reverse":
		g.Direction *= -1
		g.ActiveColor = card.Color
		g.StackMode = "none"
		g.StackAmount = 0
		g.advance(room, 1)

	case "skip":
		g.ActiveColor = card.Color
		g.StackMode = "none"
		g.StackAmount = 0
		g.advance(room, 2)

	case "shoot":
		g.ActiveColor = card.Color
		g.StackMode = "none"
		g.StackAmount = 0

		targetIdx := indexOf(room.PlayerIDs, targetID)
		if targetIdx != -1 {
			s.drawCardsLocked(room, targetID, 2)
			g.TurnIdx = targetIdx
		} else {
			g.advance(room, 1)
		}

	case "draw2":
		g.ActiveColor = card.Color
		g.StackMode = "plus2"
		g.StackAmount += 2
		g.advance(room, 1)

	case "wild_color":
		g.ActiveColor = chosenColor
		g.StackMode = "none"
		g.StackAmount = 0
		g.advance(room, 1)

	case "wild_draw4":
		g.StackMode = "plus4"
		g.StackAmount += 4
		g.advance(room, 1)

	case "wild_exchange":
		target := s.players[targetID]
		if target != nil && target.ID != player.ID {
			tmp := player.Hand
			player.Hand = target.Hand
			target.Hand = tmp
		}
		g.StackMode = "none"
		g.StackAmount = 0
		g.advance(room, 1)

	default:
		// Number card.
		g.ActiveColor = card.Color
		g.StackMode = "none"
		g.StackAmount = 0
		g.advance(room, 1)
	}
}

func (s *Server) handleMessage(c *Client, raw map[string]interface{}) {
	typ := asString(raw["type"])

	if typ == "ping" {
		s.send(c, map[string]interface{}{
			"type": "pong",
			"ts":   raw["ts"],
		})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	player := s.players[c.playerID]
	if player == nil {
		return
	}

	player.Connected = true
	player.ClientID = c.id

	switch typ {
	case "set_nickname":
		nick := strings.TrimSpace(asString(raw["nickname"]))
		if nick == "" || len(nick) > 40 {
			s.send(c, errMsg("invalid_nickname", "invalid nickname"))
			return
		}
		player.Nickname = nick
		s.send(c, map[string]interface{}{
			"type":     "nickname_set",
			"nickname": nick,
		})
		if player.RoomID != "" {
			if room := s.rooms[player.RoomID]; room != nil {
				s.broadcastRoomStateLocked(room)
			}
		}
		s.broadcastChallengesLocked()

	case "get_challenges":
		s.send(c, map[string]interface{}{
			"type":       "challenges",
			"challenges": s.challengeListLocked(),
		})

	case "create_challenge":
		if player.Nickname == "" {
			s.send(c, errMsg("need_nickname", "set nickname first"))
			return
		}

		if player.RoomID != "" {
			s.removePlayerFromRoomLocked(player)
		}

		n := asInt(raw["initialCards"], 7)
		if n < minInitialCards {
			n = minInitialCards
		}
		if n > maxInitialCards {
			n = maxInitialCards
		}

		room := &Room{
			ID:           newID(),
			Phase:        "lobby",
			CreatorID:    player.ID,
			PasswordHash: hashPassword(asString(raw["password"])),
			InitialCards: n,
			PlayerIDs:    []string{player.ID},
			CreatedAt:    time.Now(),
		}
		s.rooms[room.ID] = room
		player.RoomID = room.ID

		s.send(c, s.roomUpdateLocked(room))
		s.broadcastChallengesLocked()

	case "join_challenge":
		roomID := asString(raw["roomId"])
		room := s.rooms[roomID]
		if room == nil || room.Phase != "lobby" {
			s.send(c, errMsg("invalid_room", "room not found"))
			return
		}

		if room.PasswordHash != "" {
			if hashPassword(asString(raw["password"])) != room.PasswordHash {
				s.send(c, errMsg("wrong_password", "wrong password"))
				return
			}
		}

		if indexOf(room.PlayerIDs, player.ID) != -1 {
			player.RoomID = room.ID
			s.send(c, s.roomUpdateLocked(room))
			return
		}

		if len(room.PlayerIDs) >= maxPlayers {
			s.send(c, errMsg("room_full", "room is full"))
			return
		}

		if player.RoomID != "" {
			s.removePlayerFromRoomLocked(player)
		}

		room.PlayerIDs = append(room.PlayerIDs, player.ID)
		player.RoomID = room.ID

		s.broadcastRoomStateLocked(room)
		s.broadcastChallengesLocked()

	case "leave_room":
		if player.RoomID == "" {
			s.send(c, map[string]interface{}{"type": "room_left"})
			return
		}
		s.removePlayerFromRoomLocked(player)
		s.send(c, map[string]interface{}{"type": "room_left"})

	case "kick_player":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "lobby" {
			s.send(c, errMsg("not_in_room", "not in lobby room"))
			return
		}
		if room.CreatorID != player.ID {
			s.send(c, errMsg("not_creator", "only creator can kick"))
			return
		}

		targetID := asString(raw["playerId"])
		if targetID == player.ID || indexOf(room.PlayerIDs, targetID) == -1 {
			s.send(c, errMsg("invalid_target", "invalid target"))
			return
		}

		target := s.players[targetID]
		if target == nil {
			s.send(c, errMsg("invalid_target", "invalid target"))
			return
		}

		s.removePlayerFromRoomLocked(target)
		s.sendToPlayerLocked(targetID, map[string]interface{}{
			"type":   "room_left",
			"reason": "kicked",
		})

	case "update_challenge_settings":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "lobby" {
			s.send(c, errMsg("not_in_room", "not in lobby room"))
			return
		}
		if room.CreatorID != player.ID {
			s.send(c, errMsg("not_creator", "only creator can update settings"))
			return
		}

		n := asInt(raw["initialCards"], room.InitialCards)
		if n < minInitialCards || n > maxInitialCards {
			s.send(c, errMsg("invalid_settings", "invalid initial cards"))
			return
		}
		if len(room.PlayerIDs)*n+1 > deckTotal {
			s.send(c, errMsg("invalid_settings", "not enough cards for this setting"))
			return
		}

		room.InitialCards = n
		s.broadcastRoomStateLocked(room)
		s.broadcastChallengesLocked()

	case "start_game":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "lobby" {
			s.send(c, errMsg("not_in_room", "not in lobby room"))
			return
		}
		if room.CreatorID != player.ID {
			s.send(c, errMsg("not_creator", "only creator can start"))
			return
		}
		if len(room.PlayerIDs) < 2 {
			s.send(c, errMsg("not_enough_players", "need at least 2 players"))
			return
		}
		if len(room.PlayerIDs)*room.InitialCards+1 > deckTotal {
			s.send(c, errMsg("invalid_settings", "not enough cards for this setting"))
			return
		}

		game := &Game{
			Direction: 1,
			StackMode: "none",
		}
		room.Game = game

		deck := NewDeck()
		rand.Shuffle(len(deck), func(i, j int) {
			deck[i], deck[j] = deck[j], deck[i]
		})

		topIdx := -1
		for i, ccard := range deck {
			if ccard.Color != "wild" && isNumberValue(ccard.Value) {
				topIdx = i
				break
			}
		}
		if topIdx == -1 {
			s.send(c, errMsg("internal_error", "could not choose initial card"))
			return
		}

		game.Top = deck[topIdx]
		deck = append(deck[:topIdx], deck[topIdx+1:]...)
		game.Deck = deck
		game.ActiveColor = game.Top.Color

		for _, pid := range room.PlayerIDs {
			p := s.players[pid]
			if p == nil {
				continue
			}
			p.Hand = []Card{}
			s.drawCardsLocked(room, pid, room.InitialCards)
		}

		game.TurnIdx = rand.Intn(len(room.PlayerIDs))
		room.Phase = "playing"
		room.WinnerID = ""

		s.broadcastChallengesLocked()

		for _, pid := range room.PlayerIDs {
			s.sendToPlayerLocked(pid, map[string]interface{}{
				"type": "game_started",
			})
		}

		s.broadcastRoomStateLocked(room)
		s.broadcastHandsLocked(room)

	case "play_card":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "playing" || room.Game == nil {
			s.send(c, errMsg("invalid_room", "not in game"))
			return
		}

		g := room.Game
		if g.currentPlayerID(room) != player.ID {
			s.send(c, errMsg("not_your_turn", "not your turn"))
			return
		}

		cardID := asString(raw["cardId"])
		cardIdx := -1
		var card Card
		for i, hc := range player.Hand {
			if hc.ID == cardID {
				cardIdx = i
				card = hc
				break
			}
		}
		if cardIdx == -1 {
			s.send(c, errMsg("invalid_play", "card not found"))
			return
		}

		if !canPlayCard(g, card) {
			s.send(c, errMsg("invalid_play", "card is not playable now"))
			return
		}

		chosenColor := asString(raw["chosenColor"])
		targetID := asString(raw["targetPlayerId"])

		switch card.Value {
		case "wild_color":
			if !isValidColor(chosenColor) {
				s.send(c, errMsg("invalid_color", "choose a valid color"))
				return
			}
		case "shoot":
			if indexOf(room.PlayerIDs, targetID) == -1 {
				s.send(c, errMsg("invalid_target", "choose a valid target"))
				return
			}
		case "wild_exchange":
			if indexOf(room.PlayerIDs, targetID) == -1 {
				s.send(c, errMsg("invalid_target", "choose a valid target"))
				return
			}
			if targetID == player.ID {
				s.send(c, errMsg("invalid_target", "exchange target must be another player"))
				return
			}
		}

		player.Hand = append(player.Hand[:cardIdx], player.Hand[cardIdx+1:]...)

		s.moveTopLocked(room, card)

		// Immediate win condition: last playable card wins instantly.
		if len(player.Hand) == 0 {
			s.endGameLocked(room, player.ID)
			return
		}

		s.applyCardLocked(room, player, card, targetID, chosenColor)
		s.broadcastRoomStateLocked(room)
		s.broadcastHandsLocked(room)

	case "claim_stack":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "playing" || room.Game == nil {
			s.send(c, errMsg("invalid_room", "not in game"))
			return
		}

		g := room.Game
		if g.currentPlayerID(room) != player.ID {
			s.send(c, errMsg("not_your_turn", "not your turn"))
			return
		}
		if g.StackMode == "none" {
			s.send(c, errMsg("invalid_play", "no stack to claim"))
			return
		}

		chosenColor := asString(raw["chosenColor"])
		if g.StackMode == "plus4" && !isValidColor(chosenColor) {
			s.send(c, errMsg("invalid_color", "choose a valid color"))
			return
		}

		count := g.StackAmount
		s.drawCardsLocked(room, player.ID, count)

		if g.StackMode == "plus4" {
			g.ActiveColor = chosenColor
		}

		g.StackMode = "none"
		g.StackAmount = 0
		g.DrawnThisTurn = false
		g.advance(room, 1)

		s.broadcastRoomStateLocked(room)
		s.broadcastHandsLocked(room)

	case "draw":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "playing" || room.Game == nil {
			s.send(c, errMsg("invalid_room", "not in game"))
			return
		}

		g := room.Game
		if g.currentPlayerID(room) != player.ID {
			s.send(c, errMsg("not_your_turn", "not your turn"))
			return
		}
		if g.StackMode != "none" {
			s.send(c, errMsg("invalid_play", "use claim during stack"))
			return
		}
		if g.DrawnThisTurn {
			s.send(c, errMsg("already_drew", "already drew this turn"))
			return
		}

		s.drawCardsLocked(room, player.ID, 1)
		g.DrawnThisTurn = true

		s.broadcastRoomStateLocked(room)
		s.broadcastHandsLocked(room)

	case "pass_turn":
		room := s.rooms[player.RoomID]
		if room == nil || room.Phase != "playing" || room.Game == nil {
			s.send(c, errMsg("invalid_room", "not in game"))
			return
		}

		g := room.Game
		if g.currentPlayerID(room) != player.ID {
			s.send(c, errMsg("not_your_turn", "not your turn"))
			return
		}
		if g.StackMode != "none" {
			s.send(c, errMsg("invalid_play", "cannot pass during stack"))
			return
		}
		if !g.DrawnThisTurn {
			s.send(c, errMsg("draw_first", "draw before passing"))
			return
		}

		g.DrawnThisTurn = false
		g.advance(room, 1)

		s.broadcastRoomStateLocked(room)
		s.broadcastHandsLocked(room)

	default:
		s.send(c, errMsg("unknown_message", "unknown message type"))
	}
}

func (c *Client) readPump() {
	defer func() {
		c.server.handleDisconnect(c)
	}()

	for {
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}
		c.server.handleMessage(c, payload)
	}
}

func (c *Client) writePump() {
	defer c.conn.Close()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleDisconnect(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clients[c.id]; !ok {
		return
	}
	delete(s.clients, c.id)

	p := s.players[c.playerID]
	if p != nil && p.ClientID == c.id {
		p.Connected = false
		p.ClientID = ""
	}

	if p != nil && p.RoomID != "" {
		if room := s.rooms[p.RoomID]; room != nil {
			s.broadcastRoomStateLocked(room)
		}
	}
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || len(token) > 64 {
		token = newID()
	}

	ip := getIP(r)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.mu.Lock()

	player, exists := s.players[token]
	if !exists {
		player = &Player{
			ID:        token,
			IP:        ip,
			Connected: true,
		}
		s.players[token] = player
	}

	// Replace old connection for same player.
	if player.ClientID != "" {
		if old, ok := s.clients[player.ClientID]; ok {
			old.conn.Close()
			delete(s.clients, old.id)
		}
	}

	client := &Client{
		id:       newID(),
		conn:     conn,
		send:     make(chan []byte, 1024),
		playerID: player.ID,
		server:   s,
	}
	s.clients[client.id] = client

	player.ClientID = client.id
	player.Connected = true
	player.IP = ip

	s.send(client, map[string]interface{}{
		"type":     "welcome",
		"playerId": player.ID,
		"nickname": player.Nickname,
	})

	s.sendRoomSnapshotLocked(client)

	s.mu.Unlock()

	go client.writePump()
	go client.readPump()
}

func cardLabel(value string) string {
	switch value {
	case "reverse":
		return "⟲"
	case "skip":
		return "⊘"
	case "shoot":
		return "🎯"
	case "draw2":
		return "+2"
	case "wild_color":
		return "WILD"
	case "wild_draw4":
		return "+4"
	case "wild_exchange":
		return "⇄"
	default:
		return value
	}
}

func cardSVG(color, value string) []byte {
	bg := "#111827"
	fg := "#ffffff"

	switch color {
	case "red":
		bg = "#dc2626"
	case "yellow":
		bg = "#eab308"
		fg = "#111111"
	case "green":
		bg = "#16a34a"
	case "blue":
		bg = "#2563eb"
	case "wild":
		bg = "#111827"
	}

	label := cardLabel(value)

	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="220" height="330" viewBox="0 0 220 330">
<rect x="4" y="4" width="212" height="322" rx="18" fill="%s" stroke="#ffffff" stroke-width="8"/>
<text x="50%%" y="55%%" font-family="sans-serif" font-size="60" fill="%s" text-anchor="middle" dominant-baseline="middle">%s</text>
<text x="12%%" y="12%%" font-family="sans-serif" font-size="24" fill="%s" text-anchor="middle">%s</text>
<text x="88%%" y="88%%" font-family="sans-serif" font-size="24" fill="%s" text-anchor="middle">%s</text>
</svg>`,
		bg, fg, label, fg, label, fg, label,
	)

	return []byte(svg)
}

func cardBackSVG() []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="220" height="330" viewBox="0 0 220 330">
<rect x="4" y="4" width="212" height="322" rx="18" fill="#1f2937" stroke="#ffffff" stroke-width="8"/>
<text x="50%" y="55%" font-family="sans-serif" font-size="42" fill="#ffffff" text-anchor="middle" dominant-baseline="middle">UNO</text>
</svg>`)
}

func parseCardName(name string) (string, string, bool) {
	if name == "card_back" {
		return "back", "back", true
	}
	if strings.HasPrefix(name, "wild_") {
		return "wild", name, true
	}
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *Server) serveCard(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/cards/")
	name = filepath.Clean(name)

	if name == "." || name == "" || name == ".." || strings.ContainsAny(name, "/\\") {
		http.NotFound(w, r)
		return
	}

	if name == "card_back" {
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".svg"} {
			p := filepath.Join("assets", "cards", "card_back"+ext)
			if _, err := os.Stat(p); err == nil {
				http.ServeFile(w, r, p)
				return
			}
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write(cardBackSVG())
		return
	}

	for _, ext := range []string{".png", ".jpg", ".jpeg", ".svg"} {
		p := filepath.Join("assets", "cards", name+ext)
		if _, err := os.Stat(p); err == nil {
			http.ServeFile(w, r, p)
			return
		}
	}

	color, value, ok := parseCardName(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Write(cardSVG(color, value))
}

func main() {
	s := NewServer()

	fs := http.FileServer(http.Dir("web"))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "web/index.html")
	})

	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/ws", s.serveWS)
	http.HandleFunc("/cards/", s.serveCard)

	addr := ":8080"
	log.Println("UNO server listening on", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
