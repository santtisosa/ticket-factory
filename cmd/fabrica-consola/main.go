// consola muestra la fábrica trabajando y, salvo --solo-mirar, la arranca ella misma (fabrica run)
// para que todo viva en una sola ventana. Solo lee runs/: lo que muestra sale de los archivos.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/santtisosa/ticket-factory/internal/fab"
)

const refresh = 2 * time.Second

// Los pasos del diagrama, con el prefijo de paso que usan los eventos.
var pasos = []struct{ id, label string }{
	{"01", "entrada"}, {"02", "spec"}, {"03-builder", "builder"}, {"03-chequeo", "chequeo"},
	{"04-seguridad", "seg"}, {"04-codigo", "código"}, {"04-ux", "ux"}, {"04-ui", "ui"},
	{"05", "approver"}, {"aprobar", "aprobar"}, {"06-bibliotecario", "cerebro"},
}

// Paleta: un solo acento (azul); verde, ámbar y rojo solo dicen estado.
var (
	tinta  = lipgloss.Color("#F3EFE9")
	gris   = lipgloss.Color("#9B958F")
	borde  = lipgloss.Color("#242321")
	acento = lipgloss.Color("#4C9AFF")
	okC    = lipgloss.Color("#4ADE80")
	runC   = lipgloss.Color("#F5B841")

	text   = lipgloss.NewStyle().Foreground(tinta)
	dim    = lipgloss.NewStyle().Foreground(gris)
	brand  = lipgloss.NewStyle().Foreground(acento).Bold(true)
	okS    = lipgloss.NewStyle().Foreground(okC)
	runS   = lipgloss.NewStyle().Foreground(runC)
	badS   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F0506E"))
	titleS = lipgloss.NewStyle().Foreground(gris).Bold(true)
	panel  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borde).Padding(0, 1)
)

type tick time.Time

type loaded struct {
	tasks  []*fab.Task
	events map[string][]fab.Event
	runLog []string
	err    error
}

type model struct {
	cfg    fab.Config
	runLog string
	data   loaded
	width  int
	height int
}

func (m model) Init() tea.Cmd { return tea.Batch(m.load(), every()) }

// El refresco corre con su propio timer, aunque falle una lectura.
func every() tea.Cmd { return tea.Tick(refresh, func(t time.Time) tea.Msg { return tick(t) }) }

func (m model) load() tea.Cmd {
	return func() tea.Msg {
		ts, err := fab.LoadTasks(m.cfg.Runs)
		evs := map[string][]fab.Event{}
		for _, t := range ts {
			evs[t.Est.ID] = t.LoadEvents()
		}
		return loaded{ts, evs, tailLines(m.runLog, 6), err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if s := msg.String(); s == "q" || s == "ctrl+c" {
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tick:
		return m, tea.Batch(m.load(), every())
	case loaded:
		m.data = msg
	}
	return m, nil
}

// pill pinta un paso según el último evento que tuvo.
func pill(t *fab.Task, evs []fab.Event, id, label string) string {
	switch id {
	case "aprobar":
		switch t.Est.Estado {
		case fab.EsperandoAprobacion:
			return runS.Render("◆ " + label)
		case fab.PRAbierto:
			return okS.Render("● PR")
		}
		return dim.Render("· " + label)
	case "01":
		return okS.Render("● " + label)
	}
	last := ""
	for _, ev := range evs {
		if !strings.HasPrefix(ev.Paso, id) {
			continue
		}
		switch ev.Estado {
		case "ok", "falla", "inicio":
			last = ev.Estado
		case fab.Trabada: // el cierre de la tarea en este paso: es donde se trabó
			last = "falla"
		}
	}
	switch last {
	case "ok":
		return okS.Render("● " + label)
	case "falla":
		return badS.Render("✗ " + label)
	case "inicio":
		return runS.Render("◐ " + label)
	}
	return dim.Render("· " + label)
}

func badge(estado string) string {
	switch estado {
	case fab.EsperandoAprobacion:
		return runS.Bold(true).Render("ESPERANDO APROBACIÓN")
	case fab.EnCurso:
		return runS.Render("EN CURSO")
	case fab.Trabada:
		return badS.Render("TRABADA")
	case fab.Propuesta:
		return text.Render("PROPUESTA")
	case fab.PRAbierto:
		return okS.Render("PR ABIERTA")
	}
	return dim.Render(strings.ReplaceAll(estado, "_", " "))
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if n < 4 || len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Orden: lo que pide atención arriba, lo cerrado abajo; dentro de cada grupo, lo más nuevo primero.
func rank(e string) int {
	return map[string]int{fab.EnCurso: 0, fab.EsperandoAprobacion: 1, fab.Propuesta: 2, fab.Pendiente: 3, fab.Trabada: 4}[e] +
		map[bool]int{true: 9}[e == fab.Descartada || e == fab.PRAbierto]
}

func (m model) View() string {
	w := max(m.width, 40)
	inner := w - 4 // borde + padding del panel
	ts := slices.Clone(m.data.tasks)
	sort.SliceStable(ts, func(i, j int) bool {
		if ri, rj := rank(ts[i].Est.Estado), rank(ts[j].Est.Estado); ri != rj {
			return ri < rj
		}
		return ts[i].Est.ID > ts[j].Est.ID
	})

	count := map[string]int{}
	for _, t := range ts {
		count[t.Est.Estado]++
	}
	plural := func(n int, uno, varios string) string { return map[bool]string{true: uno, false: varios}[n == 1] }
	head := fmt.Sprintf("%s  %s %s  %s %s  %s %s  %s",
		brand.Render("TICKET-FACTORY"),
		runS.Render(fmt.Sprint(count[fab.EsperandoAprobacion])), dim.Render(plural(count[fab.EsperandoAprobacion], "espera la aprobación", "esperan la aprobación")),
		badS.Render(fmt.Sprint(count[fab.Trabada])), dim.Render(plural(count[fab.Trabada], "trabada", "trabadas")),
		text.Render(fmt.Sprint(count[fab.Propuesta])), dim.Render(plural(count[fab.Propuesta], "propuesta", "propuestas")),
		dim.Render(gastoHoy(m.cfg)))

	var rows []string
	for _, t := range ts {
		e := t.Est
		short := e.ID[len(e.ID)-4:]
		b := badge(e.Estado)
		meta := dim.Render(fmt.Sprintf("  v%d · US$%.2f", e.Vuelta, e.CostUSD))
		titleW := inner - lipgloss.Width(b) - lipgloss.Width(meta) - len(short) - 3
		left := dim.Render(short) + "  " + text.Render(clip(e.Titulo, titleW))
		gap := max(1, inner-lipgloss.Width(left)-lipgloss.Width(b)-lipgloss.Width(meta))
		line := left + strings.Repeat(" ", gap) + b + meta
		if e.Estado == fab.Descartada || e.Estado == fab.PRAbierto {
			rows = append(rows, line)
			continue
		}
		var ps []string
		for _, p := range pasos {
			ps = append(ps, pill(t, m.data.events[e.ID], p.id, p.label))
		}
		steps := lipgloss.NewStyle().Width(inner).PaddingLeft(6).Render(strings.Join(ps, "  "))
		row := line + "\n" + steps
		if e.Motivo != "" && e.Estado != fab.EsperandoAprobacion {
			row += "\n      " + dim.Render("└ "+clip(e.Motivo, inner-8))
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = append(rows, dim.Render(`Sin tareas. Para cargar una:  ./fabrica add "..."`))
	}
	linea := panel.Width(w - 2).Render(titleS.Render("LÍNEA DE MONTAJE") + "\n\n" + strings.Join(rows, "\n\n"))

	runBox := ""
	if m.runLog != "" {
		runBox = panel.Width(w - 2).Render(titleS.Render("FÁBRICA") + "\n" + dim.Render(clipLines(m.data.runLog, inner)))
	}
	used := lipgloss.Height(head) + lipgloss.Height(linea) + lipgloss.Height(runBox) + 5
	registro := panel.Width(w - 2).Render(titleS.Render("REGISTRO") + "\n" + m.logView(inner, max(3, m.height-used)))

	parts := []string{head, linea, registro}
	if runBox != "" {
		parts = append(parts, runBox)
	}
	if m.data.err != nil {
		parts = append(parts, badS.Render("error leyendo runs/: "+m.data.err.Error()))
	}
	parts = append(parts, dim.Render("q sale y apaga la fábrica · se actualiza cada 2 s"))
	return strings.Join(parts, "\n")
}

func (m model) logView(inner, n int) string {
	var all []fab.Event
	for _, evs := range m.data.events {
		all = append(all, evs...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].TS.Before(all[j].TS) })
	if len(all) > n {
		all = all[len(all)-n:]
	}
	var lines []string
	for _, ev := range all {
		st := map[string]lipgloss.Style{"ok": okS, "falla": badS, "inicio": runS}[ev.Estado]
		cost := ""
		if ev.CostUSD > 0 {
			cost = fmt.Sprintf("  US$%.2f", ev.CostUSD)
		}
		when := ev.TS.Format("15:04:05") + " " + ev.ID[len(ev.ID)-4:] + " "
		paso := fmt.Sprintf("%-17s ", ev.Paso)
		msgW := inner - len(when) - len(paso) - len(cost)
		lines = append(lines, dim.Render(when)+st.Render(paso)+text.Render(clip(ev.Mensaje, msgW))+dim.Render(cost))
	}
	if len(lines) == 0 {
		return dim.Render("todavía nada")
	}
	return strings.Join(lines, "\n")
}

func clipLines(ls []string, n int) string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = clip(l, n)
	}
	return strings.Join(out, "\n")
}

func tailLines(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	ls := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return ls
}

// startFactory arranca `fabrica run` de fondo, con su salida a un log que la consola muestra. Si ya
// hay uno corriendo (otra ventana), no arranca otro: solo mira.
func startFactory(cfg fab.Config) (*exec.Cmd, string, error) {
	logPath := filepath.Join(cfg.Runs, ".cache", "run.log")
	pidPath := filepath.Join(cfg.Runs, ".cache", "run.pid")
	if b, err := os.ReadFile(pidPath); err == nil {
		var pid int
		if _, err := fmt.Sscan(string(b), &pid); err == nil && syscall.Kill(pid, 0) == nil {
			return nil, logPath, nil
		}
	}
	f, err := os.Create(logPath)
	if err != nil {
		return nil, "", err
	}
	cmd := exec.Command(filepath.Join(cfg.Factory, "fabrica"), "run")
	cmd.Dir = cfg.Factory
	cmd.Env = fab.EntornoConToken() // NewConfig sacó el token del entorno de este proceso
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	_ = os.WriteFile(pidPath, []byte(fmt.Sprint(cmd.Process.Pid)), 0o644)
	return cmd, logPath, nil
}

func main() {
	cfg, err := fab.NewConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if i := slices.Index(os.Args, "--foto"); i > 0 && i+1 < len(os.Args) { // una pantalla, sin TUI
		var w, h int
		fmt.Sscanf(os.Args[i+1], "%dx%d", &w, &h)
		m := model{cfg: cfg, width: w, height: h}
		m.data = m.load()().(loaded)
		fmt.Println(m.View())
		return
	}
	var runner *exec.Cmd
	var logPath string
	if !slices.Contains(os.Args[1:], "--solo-mirar") {
		if runner, logPath, err = startFactory(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "no se pudo arrancar la fábrica:", err)
			os.Exit(1)
		}
	}
	_, err = tea.NewProgram(model{cfg: cfg, runLog: logPath}, tea.WithAltScreen()).Run()
	if runner != nil { // la fábrica vive lo que vive la ventana
		_ = syscall.Kill(-runner.Process.Pid, syscall.SIGINT)
		_ = runner.Wait()
		_ = os.Remove(filepath.Join(cfg.Runs, ".cache", "run.pid"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func gastoHoy(cfg fab.Config) string {
	if cfg.BudgetDayUSD > 0 {
		return fmt.Sprintf("· hoy US$%.2f de %.0f", fab.SpentToday(cfg.Runs), cfg.BudgetDayUSD)
	}
	return fmt.Sprintf("· hoy US$%.2f, sin tope", fab.SpentToday(cfg.Runs))
}
