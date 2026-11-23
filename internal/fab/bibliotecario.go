package fab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Biblio es la salida del Bibliotecario.
type Biblio struct {
	Nada       bool   `json:"nada"`
	Motivo     string `json:"motivo"`
	Escrituras []struct {
		Archivo string `json:"archivo"`
		Tipo    string `json:"tipo"`
		Resumen string `json:"resumen"`
	} `json:"escrituras"`
}

// allowedPath: rel (relativo al clon) está en lo escribible (conocimiento.escribibles: archivos, o
// carpetas terminadas en "/"). Lo hace cumplir el hook solo-cerebro.sh mientras escribe y brainLint
// antes de commitear.
func allowedPath(rel string, allowed []string) bool {
	for _, a := range allowed {
		if rel == a || (strings.HasSuffix(a, "/") && strings.HasPrefix(rel, a)) {
			return true
		}
	}
	return false
}

// Sin la barra: en una tabla el alias va escapado ([[nota\\|alias]]) y la barra no es del nombre.
var reWikilink = regexp.MustCompile(`\[\[([^\]|#\\]+)`)

// brainLint revisa lo que cambió en el clon: rutas permitidas, frontmatter con la etiqueta factory
// en notas nuevas, y en las líneas agregadas (no en todo el archivo: lo viejo no es de la fábrica)
// wikilinks que existen, la marca factory en cada viñeta de un escribible que es archivo (las
// lecciones) y la barrera entre proyectos. Devuelve los problemas; vacío = se puede commitear.
func brainLint(ctx context.Context, clone string, allowed, proyectos []string) ([]string, error) {
	// Sin TrimSpace: en --porcelain la primera columna puede ser un espacio (" M archivo").
	raw, err := gitCrudo(ctx, clone, "status", "--porcelain", "--untracked-files=all")
	out := strings.TrimRight(string(raw), "\n")
	if err != nil || out == "" {
		return nil, err
	}
	notes := noteNames(clone)
	var probs []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		status, rel := strings.TrimSpace(line[:2]), strings.Trim(line[3:], `"`)
		if !allowedPath(rel, allowed) {
			probs = append(probs, "escribió fuera de lo permitido: "+rel)
			continue
		}
		b, err := os.ReadFile(filepath.Join(clone, rel))
		if err != nil {
			continue // borrado: el hook no deja borrar, pero si pasa, git lo muestra en el diff
		}
		text := string(b)
		fm := frontmatter(text)
		if status == "??" && !(strings.Contains(fm, "\ntipo:") && strings.Contains(fm, "\nfuente:") && strings.Contains(fm, "factory")) {
			probs = append(probs, "nota nueva sin frontmatter tipo:/fuente: con la etiqueta factory: "+rel)
		}
		agregadas := text
		if status != "??" {
			diff, _ := git(ctx, clone, "diff", "-U0", "HEAD", "--", rel)
			var ls []string
			for _, l := range lineasAgregadas(diff) {
				ls = append(ls, l.Texto)
			}
			agregadas = strings.Join(ls, "\n")
		}
		for _, m := range reWikilink.FindAllStringSubmatch(agregadas, -1) {
			if !notes[strings.ToLower(strings.TrimSpace(filepath.Base(m[1])))] {
				probs = append(probs, fmt.Sprintf("wikilink roto [[%s]] en %s", m[1], rel))
			}
		}
		if slices.Contains(allowed, rel) {
			for _, l := range strings.Split(agregadas, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "-") && !strings.Contains(l, "factory") {
					probs = append(probs, "lección sin la marca factory en su fuente: "+strings.TrimSpace(l))
				}
			}
		}
		if !strings.Contains(fm, "alcance: proyecto") {
			for _, slug := range proyectos {
				if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(slug) + `\b`).MatchString(agregadas) {
					probs = append(probs, fmt.Sprintf("%s nombra al proyecto %s: una lección general no nombra proyectos (si es de ese proyecto, va con alcance: proyecto)", rel, slug))
				}
			}
		}
	}
	return probs, nil
}

// frontmatter devuelve el bloque --- del principio (con el salto inicial), o "".
func frontmatter(text string) string {
	if !strings.HasPrefix(text, "---") {
		return ""
	}
	fin := strings.Index(text[3:], "\n---")
	if fin < 0 {
		return ""
	}
	return text[3 : 3+fin+1]
}

func noteNames(clone string) map[string]bool {
	n := map[string]bool{}
	_ = filepath.WalkDir(clone, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			n[strings.ToLower(strings.TrimSuffix(filepath.Base(p), ".md"))] = true
		}
		return nil
	})
	return n
}

// commitBrain: lint verde → un commit en `cerebro`; rojo → se descarta todo lo escrito.
func commitBrain(ctx context.Context, clone, msg string, allowed, proyectos []string) (string, error) {
	probs, err := brainLint(ctx, clone, allowed, proyectos)
	if err != nil {
		return "", err
	}
	if len(probs) > 0 {
		descartarCerebro(ctx, clone)
		return "", fmt.Errorf("lint del cerebro en rojo, se descartó lo escrito: %s", strings.Join(probs, "; "))
	}
	if st, _ := git(ctx, clone, "status", "--porcelain"); st == "" {
		return "nada nuevo", nil
	}
	if _, err := git(ctx, clone, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := git(ctx, clone, "-c", "user.name=ticket-factory", "-c", "user.email=ticket-factory@localhost", "commit", "-qm", msg); err != nil {
		return "", err
	}
	return git(ctx, clone, "log", "-1", "--format=%h %s")
}

// descartarCerebro deja el clon como el último commit: lo que escribió un Bibliotecario que falló
// no puede quedar mezclado con lo próximo que escriba otro.
func descartarCerebro(ctx context.Context, clone string) {
	_, _ = git(ctx, clone, "checkout", "--", ".")
	_, _ = git(ctx, clone, "clean", "-fdq")
}

// bloquearCerebro: un solo proceso a la vez escribe o rebasa el clon del conocimiento (la línea y
// `fabrica preguntar` desde la terminal). Devuelve con qué soltarlo.
func (c Config) bloquearCerebro() (func(), error) {
	if err := os.MkdirAll(filepath.Join(c.Home, "clones"), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(c.Home, "clones", "conocimiento.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func (c Config) librarian(ctx context.Context, prompt string, escribibles []string) (Biblio, float64, error) {
	if err := c.EnsureBrain(ctx); err != nil {
		return Biblio{}, 0, err
	}
	if _, err := c.BuildIndex(); err != nil {
		return Biblio{}, 0, err
	}
	var allowed []string
	for _, a := range escribibles {
		allowed = append(allowed, filepath.Join(c.BrainClone(), a)+map[bool]string{true: "/"}[strings.HasSuffix(a, "/")])
	}
	prompt += "\n\nSolo se puede escribir en estas rutas de `conocimiento/` (conocimiento.escribibles; una carpeta termina en \"/\"): " +
		strings.Join(escribibles, ", ") + "."
	r, err := Run(ctx, Call{
		// --add-dir del clon: el enlace de la carpeta del Jefe resuelve ahí, y sin esto Claude Code
		// niega escribir "fuera" del cwd. Dónde puede escribir lo sigue decidiendo solo-cerebro.sh.
		// El cwd es el de los agentes (C3); la carpeta del cerebro y el clon van por --add-dir.
		Dir: c.dirAgentes(), AddDirs: []string{c.BrainFarm(), c.BrainClone()}, Agent: "bibliotecario", AgentsFile: c.AgentsFile, SettingsFile: c.BiblioSettings,
		Schema: schema("biblio.json"), BudgetUSD: c.BudgetCallUSD, Timeout: expertTimeout,
		AllowedTools: []string{"Read", "Grep", "Glob", "Edit", "Write", "StructuredOutput"},
		Env:          []string{"FABRICA_CEREBRO_PERMITIDO=" + strings.Join(allowed, ":")},
		Prompt:       carpetaCerebro(c.BrainFarm()) + prompt,
	})
	var b Biblio
	if err == nil {
		err = json.Unmarshal(r.Structured, &b)
	}
	if d := r.DenialSummary(); d != "" {
		fmt.Fprintln(os.Stderr, "bibliotecario: le negaron:", d)
	}
	if err == nil && len(b.Escrituras) > 0 {
		if st, _ := git(ctx, c.BrainClone(), "status", "--porcelain", "--untracked-files=all"); st == "" {
			err = fmt.Errorf("dijo que escribió %d archivo(s) pero no cambió nada (negado: %s)", len(b.Escrituras), r.DenialSummary())
		}
	}
	if err != nil {
		descartarCerebro(ctx, c.BrainClone())
	}
	return b, r.CostUSD, err
}

// Librarian cierra una tarea: lee lo que pasó y alimenta el cerebro si vale para todos.
// debeAprender: una vez por tarea; si no, cada reintento que termina TRABADA lo vuelve a llamar
// sobre casi lo mismo. Aprende la primera vez que la tarea cierra (lista para la aprobación o
// trabada); el `fabrica bibliotecario <id>` manual sigue pudiendo forzarlo.
func debeAprender(t *Task) bool {
	_, err := os.Stat(filepath.Join(t.Dir, "05-bibliotecario.json"))
	return os.IsNotExist(err)
}

func (c Config) Librarian(ctx context.Context, t *Task) {
	k, err := c.conocimiento()
	if err != nil || len(k.Escribibles) == 0 || !debeAprender(t) {
		return // sin conocimiento.repo ni conocimiento.escribibles no hay dónde aprender
	}
	spec, err := os.ReadFile(filepath.Join(t.Dir, "01-spec.md"))
	if err != nil {
		return // sin spec no hubo trabajo del que aprender
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cerró la tarea %s en estado %s (%s), en %d vuelta(s).\n\n## Spec\n%s\n", t.Est.ID, t.Est.Estado, t.Est.Motivo, t.Est.Vuelta, spec)
	// Lo que vieron todos: Builder, herramientas, cada revisor y el Approver.
	for _, pat := range []string{"02-build-v*.json", "03-herramientas-v*.json", "03-seguridad.json", "03-codigo.json", "03-ux.json", "03-ui.json", "04-approver-v*.json"} {
		files, _ := filepath.Glob(filepath.Join(t.Dir, pat))
		for _, f := range files {
			raw, _ := os.ReadFile(f)
			fmt.Fprintf(&b, "\n## %s\n%s\n", filepath.Base(f), tail(string(raw)))
		}
	}
	// Lo que les negaron a los expertos dice qué reglas de la fábrica chocaron con el trabajo.
	for _, ev := range t.LoadEvents() {
		if strings.Contains(ev.Mensaje, "le negaron") {
			fmt.Fprintf(&b, "\n- %s: %s", ev.Paso, ev.Mensaje)
		}
	}
	b.WriteString("\nLa fuente de lo que escribas es `tarea " + t.Est.ID + "`.")
	soltar, err := c.bloquearCerebro()
	if err != nil {
		t.Log("06-bibliotecario", "falla", "no se pudo tomar el clon del conocimiento: "+err.Error(), 0)
		return
	}
	defer soltar()
	t.Log("06-bibliotecario", "inicio", "bibliotecario", 0)
	res, cost, err := c.librarian(ctx, b.String(), k.Escribibles)
	if err == nil {
		var msg string
		msg, err = commitBrain(ctx, c.BrainClone(), "docs: lo aprendido en la tarea "+t.Est.ID+" (factory)", k.Escribibles, c.slugs())
		_ = t.WriteJSON("05-bibliotecario.json", res)
		if err == nil {
			t.Log("06-bibliotecario", "ok", msg, cost)
			return
		}
	}
	t.Log("06-bibliotecario", "falla", err.Error(), cost)
}

// LintCerebro le pide al Jefe que busque contradicciones y deja el informe; no cambia nada.
func (c Config) LintCerebro(ctx context.Context) (string, error) {
	r, err := c.Ask(ctx, "Revisa el cerebro en busca de reglas que se contradicen entre sí, notas viejas que contradicen una decisión "+
		"más nueva y lecciones duplicadas. Lista cada problema con las dos citas que chocan. No inventes problemas.")
	out := filepath.Join(c.Runs, ".cache", "lint-cerebro-"+time.Now().Format("2006-01-02")+".json")
	// El informe se escribe aunque falle (con el error adentro): si no, LintDue lo volvía a pedir
	// cada 30 segundos y quemaba la cuota del día.
	var v any = r
	if err != nil {
		v = map[string]string{"error": err.Error()}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if werr := os.WriteFile(out, b, 0o644); werr != nil {
		return "", werr
	}
	if err == nil && r.Sabe && len(r.Citas) > 0 {
		c.tarjetaDeLint(r)
	}
	return out, err
}

// LintDue: una vez por semana, según el último informe, si hay conocimiento.
func (c Config) LintDue() bool {
	if !c.conConocimiento() {
		return false
	}
	files, _ := filepath.Glob(filepath.Join(c.Runs, ".cache", "lint-cerebro-*.json"))
	var last time.Time
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return time.Since(last) > 7*24*time.Hour
}

func (c Config) logSide(paso, msg string, cost float64, err error) {
	estado := "ok"
	if err != nil {
		estado, msg = "falla", msg+": "+err.Error()
	}
	LogLado(c.Runs, "lado", paso, estado, msg, cost)
}

func (c Config) slugs() []string {
	var ss []string
	for _, cl := range Clientes(c.Home) {
		ss = append(ss, cl.Slug)
	}
	return ss
}

// tarjetaDeLint: las contradicciones van al tablero como propuesta (un informe en .cache no lo lee
// nadie). El operador las arregla en el repo de conocimiento y descarta la tarjeta.
func (c Config) tarjetaDeLint(r Respuesta) {
	clave := "lint-cerebro:" + time.Now().Format("2006-01-02")
	if visto(c.Runs, clave) {
		return
	}
	var b strings.Builder
	b.WriteString("El Jefe encontró contradicciones en el cerebro (repo de conocimiento):\n\n" + r.Respuesta + "\n\nCitas verificadas:\n")
	for _, ci := range r.Citas {
		fmt.Fprintf(&b, "- %s:%d %s\n", ci.Archivo, ci.Linea, ci.Texto)
	}
	if _, err := AddTask(c.Runs, b.String(), "cerebro", Propuesta, ""); err == nil {
		_ = marcarVisto(c.Runs, clave)
	}
}
