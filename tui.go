package main

import (
	"fmt"
	"os"
	"os/exec"
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

func drawDashboard(port int) {
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
	fmt.Printf(" %sSTATUS:%s %sOnline%s  │  %sPORT:%s %d  │  %sUPTIME:%s %s\n\n", bold, reset, green, reset, bold, reset, port, bold, reset, uptime)

	// Routing mappings
	fmt.Printf(" %sACTIVE MODELS & ROUTING (HIGH EFFORT)%s\n", magenta, reset)
	fmt.Printf(" ├─ %santhropic/opencode/big-pickle%s   →  %sbig-pickle%s (%sopencode%s)\n", cyan, reset, bold, reset, yellow, reset)
	fmt.Printf(" ├─ %santhropic/claude_step_3.7_flash%s →  %sstep-3.7-flash%s (%snvidia%s)\n", cyan, reset, bold, reset, yellow, reset)
	fmt.Printf(" ├─ %santhropic/claude_K_2%s            →  %skimi-k2.6%s (%snvidia%s)\n", cyan, reset, bold, reset, yellow, reset)
	fmt.Printf(" └─ %santhropic/claude_M_2.6%s            →  %smimo-v2.5-free%s (%sopencode%s)\n\n", cyan, reset, bold, reset, yellow, reset)

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
			fmt.Printf("  [%s%s%s] %s%-32s%s → %s%-20s%s │ %s │ In:%-4d Out:%-4d\n",
				gray, timeStr, reset,
				bold, log.Model, reset,
				yellow, log.Route, reset,
				statusStr,
				log.TokensIn, log.TokensOut,
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

func RunTUI(port int, stopChan chan bool) {
	tuiActive = true
	if err := setRawMode(true); err != nil {
		fmt.Printf("Failed to set raw mode: %v\n", err)
		return
	}
	defer setRawMode(false)

	defer fmt.Print("\033[?25h\033[H\033[2J") // Restore cursor, clear screen
	fmt.Print("\033[?25l")                   // Hide cursor

	keypressChan := make(chan string)
	go readKeypresses(keypressChan)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		drawDashboard(port)

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
