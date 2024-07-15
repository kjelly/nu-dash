package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/goccy/go-yaml"
	tsize "github.com/kopoli/go-terminal-size"
	lop "github.com/samber/lo/parallel"
)

type Config struct {
	CheckList []NushellIn `yaml:"checkList"`
}

type NushellIn struct {
	Title string `yaml:"title"`
	Cmd   string `yaml:"cmd"`
	Fix   string
	More  string
}

type NushellOut struct {
	Level   string
	Message string
	Details string
}

func nushell(indata NushellIn) (outdata NushellOut) {
	cmd := exec.Command("nu", "-c", indata.Cmd)
	out, err := cmd.CombinedOutput()
	fmt.Printf("%v", string(out))
	if err != nil {
		return NushellOut{"critical", err.Error(), string(out)}
	}
	err = json.Unmarshal([]byte(out), &outdata)
	if err != nil {
		return NushellOut{"critical", err.Error(), string(out)}
	}
	return outdata
}

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

type model struct {
	table table.Model
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			row := m.table.SelectedRow()
			return m, tea.Batch(
				tea.Printf("%s\n", row[2]),
				tea.Printf("%s\n", row[3]),
			)
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	return baseStyle.Render(m.table.View()) + "\n"
}

func main() {
	yamlData := `
---
checkList:
  - title: "List all files"
    cmd: "ls"
  `
	var config Config
	if err := yaml.Unmarshal([]byte(yamlData), &config); err != nil {
		tea.Printf("Yaml error")
		return
	}
	fmt.Printf("%v", config)

	rows := lop.Map(config.CheckList, func(indata NushellIn, _ int) table.Row {
		out := nushell(indata)
		fmt.Printf("%v", out)
		return table.Row{out.Level, indata.Title, out.Message, out.Details}

	})

	terminal_width := 80
	ts, err := tsize.GetSize()
	if err == nil {
		terminal_width = ts.Width
	}

	columns := []table.Column{
		{Title: "Level", Width: 10},
		{Title: "Title", Width: 30},
		{Title: "Message", Width: (terminal_width - 50) / 2},
		{Title: "Details", Width: (terminal_width - 50) / 2},
	}

	// rows = []table.Row{
	// 	{"1", "Tokyo", "Japan", "37,274,000\na\nbbb"},
	// }
	fmt.Printf("%v", rows)

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(7),
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

	m := model{t}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
