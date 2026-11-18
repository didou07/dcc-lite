package httpapi

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	commandFileName = "dcc-lite-commands.ini"
	maxCommandFile  = 1 << 20
	maxCommands     = 500
	maxCommandName  = 80
	maxCommandText  = 1000
)

type savedCommand struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

type commandsRequest struct {
	Commands []savedCommand `json:"commands"`
}

func (s *Server) commandFilePath() string {
	if s.app != nil && s.app.Profiles != nil {
		return filepath.Join(filepath.Dir(s.app.Profiles.Path()), commandFileName)
	}
	return ""
}

func (s *Server) getCommands(w http.ResponseWriter, _ *http.Request) {
	path := s.commandFilePath()
	if path == "" {
		writeError(w, 500, "command storage is unavailable")
		return
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		file = nil
	} else if err != nil {
		writeError(w, 500, "could not read the command library: "+err.Error())
		return
	}
	var data []byte
	if file != nil {
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, maxCommandFile+1))
		if err != nil {
			writeError(w, 500, "could not read the command library: "+err.Error())
			return
		}
	}
	if len(data) > maxCommandFile {
		writeError(w, 500, "the command library file exceeds the size limit")
		return
	}
	commands, err := parseCommandINI(data)
	if err != nil {
		writeError(w, 500, "could not read the command library: "+err.Error())
		return
	}
	location := path
	if s.remote != nil {
		location = "DCC Lite settings folder · " + commandFileName
	}
	writeJSON(w, 200, map[string]any{"commands": commands, "path": location})
}

func (s *Server) putCommands(w http.ResponseWriter, r *http.Request) {
	path := s.commandFilePath()
	if path == "" {
		writeError(w, 500, "command storage is unavailable")
		return
	}
	var req commandsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, 400, "invalid command library: "+err.Error())
		return
	}
	if len(req.Commands) > maxCommands {
		writeError(w, 400, fmt.Sprintf("the command library can contain up to %d commands", maxCommands))
		return
	}
	seenIDs := make(map[string]struct{}, len(req.Commands))
	for i := range req.Commands {
		item := &req.Commands[i]
		item.Name = strings.TrimSpace(item.Name)
		item.Command = strings.TrimSpace(item.Command)
		if item.Name == "" || !utf8.ValidString(item.Name) || strings.ContainsAny(item.Name, "\r\n\x00") || len([]rune(item.Name)) > maxCommandName {
			writeError(w, 400, fmt.Sprintf("command %d has an invalid name (1–%d characters)", i+1, maxCommandName))
			return
		}
		if item.Command == "" || !utf8.ValidString(item.Command) || len(item.Command) > maxCommandText || strings.ContainsAny(item.Command, "\r\n\x00") {
			writeError(w, 400, fmt.Sprintf("%q has an invalid command (1–%d characters, single line)", item.Name, maxCommandText))
			return
		}
		if item.ID == "" {
			id, err := newCommandID()
			if err != nil {
				writeError(w, 500, "could not generate a command ID")
				return
			}
			item.ID = id
		}
		if len(item.ID) > 64 || strings.ContainsAny(item.ID, "\r\n=\x00") {
			writeError(w, 400, fmt.Sprintf("%q has an invalid command ID", item.Name))
			return
		}
		if _, ok := seenIDs[item.ID]; ok {
			writeError(w, 400, "command IDs must be unique")
			return
		}
		seenIDs[item.ID] = struct{}{}
	}
	data := encodeCommandINI(req.Commands)
	if len(data) > maxCommandFile {
		writeError(w, 400, "the command library is too large to save")
		return
	}
	if err := writeCommandFile(path, data); err != nil {
		writeError(w, 500, "could not save the command library: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "count": len(req.Commands)})
}

func newCommandID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeCommandFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dcc-lite-commands-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary file: %w", err)
	}
	n, writeErr := tmp.Write(data)
	if writeErr != nil || n != len(data) {
		_ = tmp.Close()
		if writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		return fmt.Errorf("write temporary file: %w", writeErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace command file: %w", err)
	}
	return nil
}

func encodeCommandINI(commands []savedCommand) []byte {
	used := map[string]struct{}{"userscripts": {}}
	sections := make([]string, 0, len(commands))
	blocks := make([]string, 0, len(commands))
	for _, item := range commands {
		base := strings.TrimSpace(item.Name)
		base = strings.NewReplacer("[", "(", "]", ")", "=", "-", "\r", " ", "\n", " ").Replace(base)
		base = strings.TrimLeft(base, ";# ")
		if base == "" {
			base = "Command"
		}
		section := base
		for n := 2; ; n++ {
			key := strings.ToLower(section)
			if _, exists := used[key]; !exists {
				used[key] = struct{}{}
				break
			}
			section = fmt.Sprintf("%s (%d)", base, n)
		}
		sections = append(sections, section+"=1")
		blocks = append(blocks, fmt.Sprintf("[%s]\r\nname=%s\r\nid=%s\r\nline1=%s", section, item.Name, item.ID, item.Command))
	}
	var out strings.Builder
	out.WriteString("[UserScripts]\r\n")
	out.WriteString(strings.Join(sections, "\r\n"))
	out.WriteString("\r\n\r\n")
	out.WriteString(strings.Join(blocks, "\r\n\r\n"))
	out.WriteString("\r\n")
	return []byte(out.String())
}

func parseCommandINI(data []byte) ([]savedCommand, error) {
	if len(data) == 0 {
		return []savedCommand{}, nil
	}
	type section struct {
		name   string
		values map[string]string
	}
	sections := make(map[string]*section)
	var current *section
	var listedOrder []string
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(string(data), "\uFEFF")))
	scanner.Buffer(make([]byte, 4096), maxCommandFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) >= 2 && line[0] == '[' && line[len(line)-1] == ']' {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" {
				current = nil
				continue
			}
			current = &section{name: name, values: make(map[string]string)}
			sections[strings.ToLower(name)] = current
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			key = strings.ToLower(strings.TrimSpace(key))
			current.values[key] = strings.TrimSpace(value)
			if strings.EqualFold(current.name, "UserScripts") {
				listedOrder = append(listedOrder, key)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	listed := sections["userscripts"]
	if listed == nil {
		return nil, errors.New("missing [UserScripts] section")
	}
	commands := make([]savedCommand, 0, len(listedOrder))
	seen := make(map[string]bool, len(listedOrder))
	for _, key := range listedOrder {
		if listed.values[key] == "0" {
			continue
		}
		item := sections[key]
		if item == nil {
			continue
		}
		name := item.values["name"]
		if name == "" {
			name = item.name
		}
		var lines []string
		for lineNo := 1; ; lineNo++ {
			line, ok := item.values[fmt.Sprintf("line%d", lineNo)]
			if !ok {
				break
			}
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		command := strings.Join(lines, " ; ")
		if command == "" || len(command) > maxCommandText {
			continue
		}
		id := item.values["id"]
		if id == "" || seen[id] {
			var err error
			id, err = newCommandID()
			if err != nil {
				return nil, fmt.Errorf("generate command ID: %w", err)
			}
		}
		seen[id] = true
		commands = append(commands, savedCommand{ID: id, Name: name, Command: command})
	}
	return commands, nil
}
