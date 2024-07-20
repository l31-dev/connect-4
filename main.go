package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const (
	rows    = 6
	columns = 7
)

type GameState struct {
	Board      [rows][columns]int `json:"board"`
	NextPlayer int                `json:"nextPlayer"`
	Winner     int                `json:"winner"`
}

var (
	maxConnections    = 2
	activeConnections = make(map[string]int)
	mu                sync.Mutex
	connections       = make(map[string]map[string]*websocket.Conn)
	playerIDs         = make(map[string]map[string]int)
	games             = make(map[string]*GameState)
)

func generateID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 8)
	rand.Seed(time.Now().UnixNano())
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func broadcastMessage(channelID, message string) {
	for _, ws := range connections[channelID] {
		err := websocket.Message.Send(ws, message)
		if err != nil {
			fmt.Printf("Failed to send message to a client in channel %s\n", channelID)
		}
	}
}

func updateGameState(channelID string, col int, player int) bool {
	game := games[channelID]

	if game.NextPlayer != player || game.Winner != 0 {
		return false
	}

	for i := rows - 1; i >= 0; i-- {
		if game.Board[i][col] == 0 {
			game.Board[i][col] = player
			if checkWin(game.Board, player) {
				game.Winner = player
			} else {
				game.NextPlayer = 3 - player // Switch between 1 and 2
			}
			return true
		}
	}
	return false
}

func checkWin(board [rows][columns]int, player int) bool {
	// Check for 4 in a row horizontally, vertically, and diagonally
	for i := 0; i < rows; i++ {
		for j := 0; j < columns-3; j++ {
			if board[i][j] == player && board[i][j+1] == player && board[i][j+2] == player && board[i][j+3] == player {
				return true
			}
		}
	}
	for i := 0; i < rows-3; i++ {
		for j := 0; j < columns; j++ {
			if board[i][j] == player && board[i+1][j] == player && board[i+2][j] == player && board[i+3][j] == player {
				return true
			}
		}
	}
	for i := 0; i < rows-3; i++ {
		for j := 0; j < columns-3; j++ {
			if board[i][j] == player && board[i+1][j+1] == player && board[i+2][j+2] == player && board[i+3][j+3] == player {
				return true
			}
		}
	}
	for i := 3; i < rows; i++ {
		for j := 0; j < columns-3; j++ {
			if board[i][j] == player && board[i-1][j+1] == player && board[i-2][j+2] == player && board[i-3][j+3] == player {
				return true
			}
		}
	}
	return false
}

func handler(ws *websocket.Conn, channelID string) {
	defer ws.Close()

	mu.Lock()
	if activeConnections[channelID] >= maxConnections {
		mu.Unlock()
		fmt.Println("Max connections reached for channel:", channelID)
		return
	}
	connectionID := generateID()
	if connections[channelID] == nil {
		connections[channelID] = make(map[string]*websocket.Conn)
		playerIDs[channelID] = make(map[string]int)
	}
	player := activeConnections[channelID] + 1
	connections[channelID][connectionID] = ws
	playerIDs[channelID][connectionID] = player
	activeConnections[channelID]++
	if games[channelID] == nil {
		games[channelID] = &GameState{NextPlayer: 1}
	}

	playerInfoMessage := map[string]interface{}{
		"message": "Player assigned",
		"player":  player,
	}
	message, _ := json.Marshal(playerInfoMessage)
	websocket.Message.Send(ws, string(message))

	if activeConnections[channelID] == 2 {
		gameStartMessage := map[string]interface{}{
			"message":    "Game started",
			"nextPlayer": games[channelID].NextPlayer,
		}
		message, _ := json.Marshal(gameStartMessage)
		broadcastMessage(channelID, string(message))
	}

	mu.Unlock()

	fmt.Printf("New connection established in channel %s with ID: %s\n", channelID, connectionID)

	for {
		var message string
		err := websocket.Message.Receive(ws, &message)
		if err != nil {
			fmt.Printf("Connection with ID %s in channel %s closed\n", connectionID, channelID)
			break
		}
		fmt.Printf("Received message from %s in channel %s: %s\n", connectionID, channelID, message)

		var move struct {
			Column int `json:"column"`
		}
		err = json.Unmarshal([]byte(message), &move)
		if err == nil {
			mu.Lock()
			player := playerIDs[channelID][connectionID]
			if updateGameState(channelID, move.Column, player) {
				state, _ := json.Marshal(games[channelID])
				broadcastMessage(channelID, string(state))
			}
			mu.Unlock()
		}
	}

	mu.Lock()
	delete(connections[channelID], connectionID)
	delete(playerIDs[channelID], connectionID)
	activeConnections[channelID]--
	if activeConnections[channelID] == 0 {
		delete(connections, channelID)
		delete(activeConnections, channelID)
		delete(games, channelID)
		delete(playerIDs, channelID)
		fmt.Printf("Channel %s destroyed\n", channelID)
	}
	mu.Unlock()
}

func createChannel(w http.ResponseWriter, r *http.Request) {
	channelID := generateID()
	http.Redirect(w, r, "/"+channelID, http.StatusFound)
}

func serveChannel(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "./static/index.html")
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			createChannel(w, r)
		} else {
			serveChannel(w, r)
		}
	})

	http.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		channelID := r.URL.Path[len("/ws/"):]
		websocket.Handler(func(ws *websocket.Conn) {
			handler(ws, channelID)
		}).ServeHTTP(w, r)
	})

	// Serve static files
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Println("Server failed to start:", err)
	}
}
