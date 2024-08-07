package main

import (
  "bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/stopwatch"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/goccy/go-yaml"
	cli "github.com/urfave/cli/v2"
	"github.com/xyproto/ollamaclient"

	tsize "github.com/kopoli/go-terminal-size"
	lop "github.com/samber/lo/parallel"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// const GEMINI_MODEL = "gemini-1.5-flash"
const GEMINI_MODEL = "gemini-1.5-pro"

func LevelToIcon(level string) string {
	if strings.Contains(level, "debug") {
		return "🐛"
	}
	if strings.Contains(level, "info") {
		return "ℹ️"
	}
	if strings.Contains(level, "warn") {
		return "⚠️"
	}
	if strings.Contains(level, "err") {
		return "❌"
	}
	return level
}

func use(obj interface{}) {
}

type Config struct {
	Tasks        []NushellIn `yaml:"tasks"`
	Interval     int         `yaml:"interval"`
	Refresh      bool        `yaml:"refresh"`
	Template     string      `yaml:"template"`
	Env          []string    `yaml:"env"`
	IgnoreStderr bool        `yaml:"ignore_stderr"`
}

type NushellIn struct {
	Name           string `yaml:"name"`
	Cmd            string `yaml:"cmd"`
	Fix            string
	More           string
	Raw            bool     `yaml:"raw"`
	Json           bool     `yaml:"json"`
	Direnv         bool     `yaml:"direnv"`
	ErrorIf        string   `yaml:"error_if"`
	MessageFormat  string   `yaml:"message_format"`
	Actions        []string `yaml:"actions"`
	Workdir        string   `yaml:"workdir"`
	IgnoreStderr   bool     `yaml:"ignore_stderr"`
	PromptData     string   `yaml:"prompt_data"`
	PromptTemplate string   `yaml:"prompt_template"`
}

type NushellOut struct {
	Level   string
	Message string
	Details string
}

func RunExternal(cmds []string, data string, workdir string, env []string) (string, string, error) {
	cmd := exec.Command(cmds[0], cmds[1:]...)
	cmd.Dir = workdir
	cmd.Env = env
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	stdin, _ := cmd.StdinPipe()
	err := cmd.Start()
	if err != nil {
		return "", "", err
	}
	io.WriteString(stdin, data)
	stdin.Close()
	err = cmd.Wait()
	return outb.String(), errb.String(), err
}

func RunExternalCombine(cmds []string, workdir string, env []string) (string, error) {
	cmd := exec.Command(cmds[0], cmds[1:]...)
	cmd.Dir = workdir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func nushell(indata NushellIn, config *Config) (outdata NushellOut) {
	c := []string{"nu", "-c", fmt.Sprintf(config.Template, indata.Cmd)}
	if indata.Direnv {
		c = append([]string{"direnv", "exec", "."}, c...)
	}
	out, stderr, err := RunExternal(c, "", indata.Workdir, config.Env)
	if indata.IgnoreStderr {
		stderr = ""
	}
	tea.LogToFile("debug.log", out)
	tea.LogToFile("debug.log", stderr)
	if err != nil {
		return NushellOut{"critical", err.Error(), out + stderr}
	}
	if !indata.Json {
		message := out
		details := out + stderr
		if indata.MessageFormat != "" {
			formatStdout, formatStderr, err := RunExternal([]string{"nu", "--stdin", "-c", indata.MessageFormat}, out, indata.Workdir, config.Env)
			if err == nil {
				message = formatStdout
			} else {
				details = details + formatStdout + formatStderr + err.Error()
			}
		}
		if indata.ErrorIf == "" {
			return NushellOut{"info", message, details}
		}
		errorIfStdoutString, errorIfStderrString, err := RunExternal([]string{"nu", "--stdin", "-c", indata.ErrorIf}, out, indata.Workdir, config.Env)
		if strings.Contains(strings.ToLower(errorIfStdoutString), "true") {
			return NushellOut{"error", message, details + errorIfStdoutString + errorIfStderrString}
		} else if err != nil {
			return NushellOut{"critical", message, details + errorIfStdoutString + errorIfStderrString + err.Error()}
		}
		return NushellOut{"info", message, details}
	}

	err = json.Unmarshal([]byte(out), &outdata)
	if err != nil {
		return NushellOut{"critical", err.Error(), string(out)}
	}
	return outdata
}

type editorFinishedMsg struct{ err error }

func openEditor() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	c := exec.Command(editor, "./nu-dash.yaml")
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{err}
	})
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

type model struct {
	table      *table.Model
	config     Config
	stopwatch  stopwatch.Model
	ConfigPath string
	Env        []string
}

func (m model) Init() tea.Cmd {
	return tea.Sequence(
		tea.ClearScreen,
		m.stopwatch.Init(),
		m.stopwatch.Start(),
	)
}

func (m model) runAction(actionIndex int) (tea.Model, tea.Cmd) {
	index, _ := strconv.Atoi(m.table.SelectedRow()[0])
	nushellIn := m.config.Tasks[index]
	if actionIndex >= len(nushellIn.Actions) {
		return m, nil
	}
	action := nushellIn.Actions[actionIndex]
	return m, tea.Sequence(
		tea.ExecProcess(exec.Command("nu", "-c", fmt.Sprintf("%s\ninput -n 1 'Press any key to continue'", action)), func(err error) tea.Msg {
			return tea.Printf("Run process with error %s", err)
		}),
		tea.ClearScreen,
	)
}

type ReloadConfigMsg struct{}

func ReloadConfigCmd() tea.Msg { return ReloadConfigMsg{} }

func (m *model) ReloadConfig() {
	yamlData, _ := os.ReadFile(m.ConfigPath)
	var config Config
	if err := yaml.Unmarshal([]byte(yamlData), &config); err == nil {
		if config.Template == "" {
			config.Template = "%s"
		}
		config.Env = append(os.Environ(), append(config.Env, m.Env...)...)
		m.config = config
	}
	if m.config.IgnoreStderr {
		for i := range m.config.Tasks {
			m.config.Tasks[i].IgnoreStderr = true
		}
	}
	m.table = GenerateRows(m.config)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.stopwatch.Update(msg)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.table.SetWidth(msg.Width)
		m.table.SetHeight(msg.Height / 2)
	case ReloadConfigMsg:
		m.ReloadConfig()
		return m, tea.ClearScreen

	case tea.KeyMsg:
		switch msg.String() {
		case "a", "m":
			index, _ := strconv.Atoi(m.table.SelectedRow()[0])
			actions, _ := yaml.Marshal(m.config.Tasks[index].Actions)
			return m, tea.Printf("%s\n", actions)
		case "1", "7":
			return m.runAction(0)
		case "2", "8":
			return m.runAction(1)
		case "3", "9":
			return m.runAction(2)
		case "4", "0":
			return m.runAction(3)
		case "5":
			return m.runAction(4)
		case "6":
			return m.runAction(6)
		case "t":
			return m, tea.Sequence(
				tea.Printf("%s\n", tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: false}),
			)

		case "e", "o":
			return m, tea.Sequence(openEditor(), ReloadConfigCmd)
		case "y":
			return m, tea.Sequence(
				tea.Printf("%v\n", m.Env),
			)
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r", "h", "alt+r", "ctrl+s":
			m.ReloadConfig()
			return m, nil
		case "c":
			index, _ := strconv.Atoi(m.table.SelectedRow()[0])
			task := m.config.Tasks[index]
			prompt := task.PromptData
			o1, o2, _ := RunExternal([]string{"nu", "-c", prompt}, "", ".", m.config.Env)
			return m, tea.Sequence(
				// tea.Printf("%s\n", chat(fmt.Sprintf("Anything wrong about the system? \n%s", o1+o2), "llama3:latest")),
				tea.Printf("%s\n", chat(fmt.Sprintf(task.PromptTemplate, o1+o2), "gemma2:27b")),
			)
		case "g":
			index, _ := strconv.Atoi(m.table.SelectedRow()[0])
			task := m.config.Tasks[index]
			prompt := task.PromptData
			o1, o2, _ := RunExternal([]string{"nu", "-c", prompt}, "", ".", m.config.Env)
			return m, tea.Sequence(
				// tea.Printf("%s\n", chat(fmt.Sprintf("Anything wrong about the system? \n%s", o1+o2), "llama3:latest")),
				tea.Printf("%s\n", gemini(fmt.Sprintf(task.PromptTemplate, o1+o2), GEMINI_MODEL)),
			)
		case "enter", "l":
			row := m.table.SelectedRow()
			return m, tea.Sequence(
				tea.Printf("%s\n", row[4]),
			)
		}
	}
	newTable, cmd := m.table.Update(msg)
	m.table = &newTable
	return m, cmd
}

func (m model) View() string {
	return baseStyle.Render(m.table.View()) + "\n"
}

func GenerateRows(config Config) *table.Model {
	rows := lop.Map(config.Tasks, func(indata NushellIn, index int) table.Row {
		out := nushell(indata, &config)
		return table.Row{strconv.Itoa(index), LevelToIcon(out.Level), indata.Name, out.Message, out.Details}
	})

	terminalWidth := 80
	terminalHeight := 15
	ts, err := tsize.GetSize()
	if err == nil {
		terminalWidth = ts.Width
		terminalHeight = ts.Height / 2
	}

	columns := []table.Column{
		{Title: "Index", Width: 5},
		{Title: "Level", Width: 10},
		{Title: "Name", Width: 30},
		{Title: "Message", Width: (terminalWidth - 60) / 2},
		{Title: "Details", Width: (terminalWidth - 60) / 2},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(terminalHeight),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)
	return &t
}

func main() {
	app := &cli.App{
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Value:   "./nu-dash.yaml",
				Usage:   "config file",
			},
			&cli.StringFlag{
				Name:    "workdir",
				Aliases: []string{"w"},
				Value:   ".",
				Usage:   "working directory",
			},
			&cli.StringSliceFlag{
				Name:    "env",
				Aliases: []string{"e"},
				Usage:   "working directory",
			},
		},
		Action: func(cCtx *cli.Context) error {
			configPath := cCtx.String("config")
			os.Chdir(cCtx.String("workdir"))
			m := model{stopwatch: stopwatch.NewWithInterval(time.Second), ConfigPath: configPath, Env: cCtx.StringSlice("env")}
			m.ReloadConfig()

			program := tea.NewProgram(m)
			use(m)
			use(program)

			fmt.Printf("%v", tea.Key{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: false})

			if _, err := program.Run(); err != nil {
				fmt.Println("Error running program:", err)
				os.Exit(1)
			}
			program.Wait()
			return nil
		},
	}
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func chat(prompt string, model string) string {
	oc := ollamaclient.NewWithModel(model)

	oc.Verbose = true

	if err := oc.PullIfNeeded(); err != nil {
		return err.Error()
	}

	output, err := oc.GetOutput(prompt)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("\n%s\n", strings.TrimSpace(output))
}
func gemini(prompt string, modelName string) string {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(os.Getenv("GEMINI_API_KEY")))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	model := client.GenerativeModel(modelName)
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		log.Fatal(err)
	}
	return responseToString(resp)
	
}

func responseToString(resp *genai.GenerateContentResponse) string {
	var ret string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			ret = ret + fmt.Sprintf("%v", part)
		}
	}
	return ret + "\n---"
}
