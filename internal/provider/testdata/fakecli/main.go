package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if argsPath := os.Getenv("FAKECLI_ARGS"); argsPath != "" {
		data, err := json.Marshal(os.Args[1:])
		if err != nil {
			os.Exit(90)
		}
		if err := os.WriteFile(argsPath, data, 0o600); err != nil {
			os.Exit(91)
		}
	}

	if sleepRaw := os.Getenv("FAKECLI_SLEEP"); sleepRaw != "" {
		if d, err := time.ParseDuration(sleepRaw); err == nil {
			time.Sleep(d)
		} else if secs, err := strconv.Atoi(sleepRaw); err == nil {
			time.Sleep(time.Duration(secs) * time.Second)
		}
	}

	if stderrMsg := os.Getenv("FAKECLI_STDERR"); stderrMsg != "" {
		fmt.Fprint(os.Stderr, stderrMsg)
	}

	if os.Getenv("FAKECLI_ECHO") == "1" {
		runEcho()
		return
	}

	if stdinPath := os.Getenv("FAKECLI_STDIN"); stdinPath != "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(92)
		}
		if err := os.WriteFile(stdinPath, data, 0o600); err != nil {
			os.Exit(93)
		}
	}

	if fixPath := os.Getenv("FAKECLI_FIXTURE"); fixPath != "" {
		f, err := os.Open(fixPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakecli: open fixture: %v\n", err)
			os.Exit(94)
		}
		_, _ = io.Copy(os.Stdout, f)
		_ = f.Close()
	}

	if exitRaw := os.Getenv("FAKECLI_EXIT"); exitRaw != "" {
		code, err := strconv.Atoi(exitRaw)
		if err == nil {
			os.Exit(code)
		}
	}
}

func runEcho() {
	initLine := `{"type":"system","subtype":"init","session_id":"echo-1","model":"opus"}`
	fmt.Println(initLine)

	var stdinOut *os.File
	if stdinPath := os.Getenv("FAKECLI_STDIN"); stdinPath != "" {
		var err error
		stdinOut, err = os.Create(stdinPath)
		if err != nil {
			os.Exit(95)
		}
		defer stdinOut.Close()
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if stdinOut != nil {
				_, _ = stdinOut.Write(line)
			}
			trimmed := strings.TrimRight(string(line), "\r\n")
			if trimmed != "" {
				content := extractUserContent(trimmed)
				reply := "eco: " + content

				asst := map[string]any{
					"type":       "assistant",
					"session_id": "echo-1",
					"message": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": reply},
						},
					},
				}
				if b, err := json.Marshal(asst); err == nil {
					fmt.Println(string(b))
				}

				res := map[string]any{
					"type":       "result",
					"subtype":    "success",
					"is_error":   false,
					"result":     reply,
					"session_id": "echo-1",
				}
				if b, err := json.Marshal(res); err == nil {
					fmt.Println(string(b))
				}
			}
		}
		if err != nil {
			break
		}
	}
}

func extractUserContent(line string) string {
	var payload struct {
		Type    string `json:"type"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err == nil && payload.Message.Content != "" {
		return payload.Message.Content
	}
	return line
}
