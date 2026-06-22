package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"
)

type LogEntry struct {
	Timestamp time.Time
	Model     string
	Route     string
	Status    int
	TokensIn  int
	TokensOut int
	Budget    int
	Effort    string
	CostUSD   float64
}

var (
	tuiLogs   []LogEntry
	tuiLogsMu sync.Mutex
	tuiActive bool
	startTime = time.Now()
)

func AddTUILog(entry LogEntry) {
	tuiLogsMu.Lock()
	defer tuiLogsMu.Unlock()
	tuiLogs = append(tuiLogs, entry)
	if len(tuiLogs) > 15 {
		tuiLogs = tuiLogs[1:]
	}

	f, err := os.OpenFile("/Users/kabir/acc/test_runs.jsonl", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		defer f.Close()
		type jLine struct {
			Timestamp string  `json:"timestamp"`
			Model     string  `json:"model"`
			Route     string  `json:"route"`
			Status    int     `json:"status"`
			TokensIn  int     `json:"tokens_in"`
			TokensOut int     `json:"tokens_out"`
			Budget    int     `json:"budget"`
			Effort    string  `json:"effort"`
			CostUSD   float64 `json:"cost_usd"`
		}
		row := jLine{
			Timestamp: entry.Timestamp.Format(time.RFC3339),
			Model:     entry.Model,
			Route:     entry.Route,
			Status:    entry.Status,
			TokensIn:  entry.TokensIn,
			TokensOut: entry.TokensOut,
			Budget:    entry.Budget,
			Effort:    entry.Effort,
			CostUSD:   entry.CostUSD,
		}
		if b, err := json.Marshal(row); err == nil {
			f.Write(append(b, '\n'))
		}
	}
}

func setRawMode(raw bool) error {
	var cmd *exec.Cmd
	if raw {
		cmd = exec.Command("stty", "raw", "-echo")
	} else {
		cmd = exec.Command("stty", "-raw", "echo")
	}
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func readKeypresses(ch chan<- string) {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			break
		}
		ch <- string(buf[0])
	}
}

// orderedRoutes returns config routes with the common slots first (opus,
// sonnet, haiku) and any remaining slots sorted alphabetically, so the
// dashboard renders the same order every refresh instead of Go's random
// map iteration order.
func orderedRoutes(routes map[string]Route) []string {
	preferred := []string{"opus", "sonnet", "haiku"}
	seen := map[string]bool{}
	var slots []string
	for _, p := range preferred {
		if _, ok := routes[p]; ok {
			slots = append(slots, p)
			seen[p] = true
		}
	}
	var rest []string
	for slot := range routes {
		if !seen[slot] {
			rest = append(rest, slot)
		}
	}
	sort.Strings(rest)
	return append(slots, rest...)
}

func drawDashboard(cfg *Config) {
	cyan := "\033[1;36m"
	green := "\033[1;32m"
	yellow := "\033[1;33m"
	magenta := "\033[1;35m"
	red := "\033[1;31m"
	gray := "\033[1;30m"
	reset := "\033[0m"
	bold := "\033[1m"

	// Clear screen & home cursor
	fmt.Print("\033[H\033[2J")

	// Print header
	fmt.Printf("%s┌────────────────────────────────────────────────────────┐%s\n", cyan, reset)
	fmt.Printf("%s│ %s%s             ▲ C C  P R O X Y  D A S H B O A R D %s%s             │%s\n", cyan, bold, green, reset, cyan, reset)
	fmt.Printf("%s└────────────────────────────────────────────────────────┘%s\n", cyan, reset)

	// Server info
	uptime := time.Since(startTime).Round(time.Second)
	fmt.Printf(" %sSTATUS:%s %sOnline%s  │  %sPORT:%s %d  │  %sUPTIME:%s %s\n\n", bold, reset, green, reset, bold, reset, cfg.Port, bold, reset, uptime)

	// Routing mappings — read live from the loaded config so the dashboard
	// always reflects config.json instead of stale hardcoded names.
	fmt.Printf(" %sACTIVE MODELS & ROUTING%s\n", magenta, reset)
	slots := orderedRoutes(cfg.Routes)
	for i, slot := range slots {
		branch := "├─"
		if i == len(slots)-1 {
			branch = "└─"
		}
		r := cfg.Routes[slot]
		fmt.Printf(" %s %s%-8s%s →  %s%s%s (%s%s%s)\n", branch, cyan, slot, reset, bold, r.Model, reset, yellow, r.Provider, reset)
	}
	fmt.Println()

	// Live logs header
	fmt.Printf(" %sLIVE REQ LOGS (LAST 15)%s\n", magenta, reset)
	fmt.Printf(" %s──────────────────────────────────────────────────────────────────────────%s\n", gray, reset)

	// Draw Logs
	tuiLogsMu.Lock()
	if len(tuiLogs) == 0 {
		fmt.Printf("  %sNo requests received yet. Listening for connections...%s\n", gray, reset)
	} else {
		for _, log := range tuiLogs {
			statusStr := fmt.Sprintf("%d OK", log.Status)
			if log.Status >= 400 {
				statusStr = fmt.Sprintf("%s%d ERR%s", red, log.Status, reset)
			} else {
				statusStr = fmt.Sprintf("%s%d OK%s", green, log.Status, reset)
			}
			timeStr := log.Timestamp.Format("15:04:05")
			costStr := ""
			if log.CostUSD > 0 {
				costStr = fmt.Sprintf(" │ %s$%.4f%s", green, log.CostUSD, reset)
			}
			fmt.Printf("  [%s%s%s] %s%-32s%s → %s%-20s%s │ %s │ In:%-4d Out:%-4d%s\n",
				gray, timeStr, reset,
				bold, log.Model, reset,
				yellow, log.Route, reset,
				statusStr,
				log.TokensIn, log.TokensOut,
				costStr,
			)
		}
	}
	tuiLogsMu.Unlock()

	// Keep footer layout aligned
	for i := len(tuiLogs); i < 15; i++ {
		fmt.Println()
	}

	// Footer controls
	fmt.Printf("\n %s──────────────────────────────────────────────────────────────────────────%s\n", gray, reset)
	fmt.Printf(" %s[C]%s Clear Logs   │   %s[R]%s Restart   │   %s[Q / Ctrl+C]%s Quit TUI\n", bold, reset, bold, reset, bold, reset)
}

func RunTUI(cfg *Config, stopChan chan bool) {
	tuiActive = true
	if err := setRawMode(true); err != nil {
		fmt.Printf("Failed to set raw mode: %v\n", err)
		return
	}
	defer setRawMode(false)

	defer fmt.Print("\033[?25h\033[H\033[2J") // Restore cursor, clear screen
	fmt.Print("\033[?25l")                    // Hide cursor

	keypressChan := make(chan string)
	go readKeypresses(keypressChan)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		drawDashboard(cfg)

		select {
		case <-ticker.C:
			// Refresh uptime & live data
		case key := <-keypressChan:
			switch key {
			case "q", "Q", "\x03": // Q or Ctrl+C
				stopChan <- true
				return
			case "c", "C":
				tuiLogsMu.Lock()
				tuiLogs = nil
				tuiLogsMu.Unlock()
			case "r", "R":
				fmt.Print("\033[H\033[2J")
				fmt.Println("Triggering proxy restart via acc-restart...")
				setRawMode(false)
				exec.Command("acc-restart").Run()
				os.Exit(0)
			}
		}
	}
}
