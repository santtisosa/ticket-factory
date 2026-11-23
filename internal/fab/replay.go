package fab

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Replay: vuelve a decidir las vueltas ya corridas con las reglas de hoy, sin llamar a ningún agente.
// Es el QA de la fábrica: cada falla real queda como caso de regresión del approver (Anthropic,
// "Demystifying evals": arrancar con 20-50 tareas sacadas de fallas reales).
//
// ponytail: reconstruye la entrada del approver desde 04-approver-vN.json (los hallazgos de esa
// vuelta y sus motivos). Lo que depende del código (chequeo rojo, tests que no corrieron, Builder
// que no terminó) no se puede re-juzgar y se mantiene; queda contado aparte como "de código".

// VueltaReplay es una vuelta re-decidida.
type VueltaReplay struct {
	Tarea, Titulo   string
	Vuelta          int
	Antes, Ahora    bool
	MotivosDeCodigo []string // los que no dependen de las reglas del approver
	Bloquean        int      // hallazgos que siguen bloqueando con las reglas de hoy
}

// prefijosDeCodigo son motivos que salen del código o de la infra, no de las reglas del approver.
var prefijosDeCodigo = []string{"chequeo rojo", "el Builder no terminó", "tocó un test", "no pude", "no se pudo"}

// ReplayTarea re-decide cada vuelta guardada de una tarea.
func ReplayTarea(dir string) ([]VueltaReplay, error) {
	t := &Task{Dir: dir}
	var spec Spec
	if err := t.ReadJSON("01-spec.json", &spec); err != nil {
		// Una corrida archivada (corrida-N/) usa la spec de su tarea.
		if err := (&Task{Dir: filepath.Dir(dir)}).ReadJSON("01-spec.json", &spec); err != nil {
			return nil, err
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "04-approver-v*.json"))
	sort.Strings(files)
	var out []VueltaReplay
	var previos map[string]bool
	for _, f := range files {
		var orig Decision
		if err := t.ReadJSON(filepath.Base(f), &orig); err != nil {
			return out, err
		}
		var build Build
		if t.ReadJSON(fmt.Sprintf("02-build-v%d.json", orig.Vuelta), &build) != nil {
			build = Build{Listo: true}
		}
		checks, reviews, codigo := reconstruir(orig, &build)
		ahora := Decide(orig.Vuelta, spec, build, checks, reviews, nil, previos)
		out = append(out, VueltaReplay{
			Tarea: filepath.Base(dir), Titulo: spec.Titulo, Vuelta: orig.Vuelta,
			Antes: orig.Pasa, Ahora: ahora.Pasa, MotivosDeCodigo: codigo, Bloquean: len(ahora.Bloquean),
		})
		previos = HuellasPrevias(ahora.Bloquean, ahora.NuevosMenores)
	}
	return out, nil
}

// reconstruir arma checks y revisiones desde una decisión grabada. Los motivos de código vuelven
// como un chequeo rojo (siguen frenando); los "no entregó" de un revisor, como su error.
func reconstruir(d Decision, build *Build) ([]Check, []Review, []string) {
	var checks []Check
	var codigo []string
	revs := map[string]*Review{}
	rev := func(name string) *Review {
		if revs[name] == nil {
			revs[name] = &Review{Revisor: name, Verificado: []string{"replay"}}
		}
		return revs[name]
	}
	for _, m := range d.Motivos {
		switch {
		case strings.HasPrefix(m, "el Builder no terminó"):
			build.Listo, build.Bloqueo = false, strings.TrimPrefix(m, "el Builder no terminó: ")
			codigo = append(codigo, primeraLinea(m))
		case strings.HasPrefix(m, "el revisor ") && strings.Contains(m, " no entregó: "):
			name := strings.TrimPrefix(m[:strings.Index(m, " no entregó: ")], "el revisor ")
			rev(name).Err = m[strings.Index(m, " no entregó: ")+len(" no entregó: "):]
		case tienePrefijo(m, prefijosDeCodigo):
			checks = append(checks, Check{Repo: "replay", Salida: m})
			codigo = append(codigo, primeraLinea(m))
		}
		// "CA-n no tiene test" y "dio falla sin un hallazgo" se recalculan solos con las reglas de hoy.
	}
	for _, grupo := range [][]Hallazgo{d.Bloquean, d.AConfirmar, d.Descartado} {
		for _, h := range grupo {
			r := rev(h.Revisor)
			r.Hallazgos = append(r.Hallazgos, h)
		}
	}
	var reviews []Review
	for _, r := range revs {
		reviews = append(reviews, *r)
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].Revisor < reviews[j].Revisor })
	return checks, reviews, codigo
}

func tienePrefijo(s string, ps []string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func primeraLinea(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 140 {
		s = s[:140] + "…"
	}
	return s
}

// ReplayTodo re-decide todas las tareas de runs/ cuyo id empieza con prefijo.
func ReplayTodo(runs, prefijo string) ([]VueltaReplay, error) {
	ents, err := os.ReadDir(runs)
	if err != nil {
		return nil, err
	}
	var out []VueltaReplay
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefijo) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(runs, e.Name())
		corridas, _ := filepath.Glob(filepath.Join(dir, "corrida-*"))
		sort.Strings(corridas)
		for _, d := range append(corridas, dir) {
			vs, err := ReplayTarea(d)
			if err != nil {
				continue // sin spec o sin vueltas: nada que re-decidir
			}
			for i := range vs {
				if d != dir {
					vs[i].Tarea = e.Name() + "/" + filepath.Base(d)
				}
			}
			out = append(out, vs...)
		}
	}
	return out, nil
}

// TablaReplay resume el replay: una fila por vuelta y el total de lo que cambió.
func TablaReplay(vs []VueltaReplay) string {
	var b strings.Builder
	pasa := func(ok bool) string {
		if ok {
			return "pasa"
		}
		return "no pasa"
	}
	antes, ahora, solo := 0, 0, 0
	for _, v := range vs {
		marca := ""
		switch {
		case !v.Antes && v.Ahora:
			marca, ahora = " ← ahora pasa", ahora+1
		case v.Antes && !v.Ahora:
			marca, antes = " ← ahora NO pasa", antes+1
		case !v.Ahora && len(v.MotivosDeCodigo) > 0 && v.Bloquean == 0:
			marca, solo = " (frena solo por código)", solo+1
		}
		fmt.Fprintf(&b, "%s v%d  antes: %-7s ahora: %-7s bloquean %d%s\n", v.Tarea, v.Vuelta, pasa(v.Antes), pasa(v.Ahora), v.Bloquean, marca)
		for _, m := range v.MotivosDeCodigo {
			fmt.Fprintf(&b, "    código: %s\n", m)
		}
	}
	fmt.Fprintf(&b, "\n%d vueltas: %d pasan ahora y antes no, %d pasaban y ahora no, %d frenan solo por código o infra.\n",
		len(vs), ahora, antes, solo)
	return b.String()
}

// archivarCorrida: cuando una tarea vuelve a correr, lo de la corrida anterior (builds, revisiones,
// decisiones) pasa a corrida-N/ en vez de pisarse. Así el replay y el Bibliotecario ven la historia.
func archivarCorrida(t *Task) error {
	var mover []string
	for _, pat := range []string{"02-build-v*.json", "03-*.json", "04-approver-v*.json", "ci-reintento.json"} {
		fs, _ := filepath.Glob(filepath.Join(t.Dir, pat))
		mover = append(mover, fs...)
	}
	if len(mover) == 0 {
		return nil
	}
	n := 1
	for ; ; n++ {
		if _, err := os.Stat(filepath.Join(t.Dir, fmt.Sprintf("corrida-%d", n))); os.IsNotExist(err) {
			break
		}
	}
	dst := filepath.Join(t.Dir, fmt.Sprintf("corrida-%d", n))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range mover {
		if err := os.Rename(f, filepath.Join(dst, filepath.Base(f))); err != nil {
			return err
		}
	}
	return nil
}
