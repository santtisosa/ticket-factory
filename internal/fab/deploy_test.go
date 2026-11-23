package fab

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tareaConRepos(t *testing.T, estado string, repos ...string) (string, *Task) {
	t.Helper()
	home := homeDePrueba(t)
	runs := filepath.Join(home, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	tk, err := AddTask(runs, "x", "ticket", estado, repos[0])
	if err != nil {
		t.Fatal(err)
	}
	s := specWith("CA-1")
	s.Repos = repos
	if err := tk.WriteJSON("01-spec.json", s); err != nil {
		t.Fatal(err)
	}
	return home, tk
}

// Una tarea mergeada de una app suelta queda pendiente de deploy; las demás, mergeadas (su trigger despliega).
func TestEstadoTrasMerge(t *testing.T) {
	_, acme := tareaConRepos(t, CIVerde, "acme")
	_, demo := tareaConRepos(t, CIVerde, "demo-backend", "demo-frontend")
	if got := estadoTrasMerge(acme); got != DesplieguePendiente {
		t.Fatalf("acme: %s", got)
	}
	if got := estadoTrasMerge(demo); got != Integrada {
		t.Fatalf("demo: %s", got)
	}
}

// Un deploy fallido no se reintenta antes de deployReintento.
func TestDebeIntentar(t *testing.T) {
	_, tk := tareaConRepos(t, DesplieguePendiente, "acme")
	ahora := time.Now()
	if !debeIntentar(tk, ahora) {
		t.Fatal("sin intentos previos, se intenta")
	}
	if err := tk.WriteJSON(deployArchivo, map[string]DeployRepo{"acme": {Ultimo: ahora.Add(-5 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if debeIntentar(tk, ahora) {
		t.Fatal("falló hace 5 minutos: espera")
	}
	if !debeIntentar(tk, ahora.Add(deployReintento)) {
		t.Fatal("pasado el tiempo, se reintenta")
	}
}

// `fabrica desplegar`: solo tareas mergeadas de apps sueltas; el seco no corre nada y dice qué correría.
func TestDesplegarValidaYSeco(t *testing.T) {
	ctx := context.Background()
	home, tk := tareaConRepos(t, Integrada, "acme")
	c := Config{Home: home, Runs: filepath.Dir(tk.Dir), Factory: filepath.Join(home, "repos", "fabrica")}
	lines, err := c.Desplegar(ctx, tk.Est.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	todo := strings.Join(lines, "\n")
	for _, quiero := range []string{"./desplegar.sh", "FABRICA_SHA", "https://acme.example.com/", "clona el main de GitHub", "tiene el merge"} {
		if !strings.Contains(todo, quiero) {
			t.Errorf("el seco no dice %q:\n%s", quiero, todo)
		}
	}
	if got, _ := findTask(c.Runs, tk.Est.ID); got.Est.Estado != Integrada {
		t.Fatalf("el seco no cambia el estado: %s", got.Est.Estado)
	}

	_, abierta := tareaConRepos(t, PRAbierto, "acme")
	c2 := Config{Runs: filepath.Dir(abierta.Dir)}
	if _, err := c2.Desplegar(ctx, abierta.Est.ID, true); err == nil {
		t.Fatal("una PR abierta no se despliega")
	}
	_, demo := tareaConRepos(t, Integrada, "demo-backend")
	c3 := Config{Runs: filepath.Dir(demo.Dir)}
	if _, err := c3.Desplegar(ctx, demo.Est.ID, true); err == nil || !strings.Contains(err.Error(), "ningún repo con deploy") {
		t.Fatalf("un repo sin deploy no se despliega: %v", err)
	}
}

// H1: no se despliega un main que todavía no tiene el merge de la PR (squash o merge común).
func TestTieneElMerge(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	corre := func(args ...string) string {
		t.Helper()
		out, err := git(ctx, dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	corre("init", "-q")
	corre("commit", "-q", "--allow-empty", "-m", "viejo")
	viejo := corre("rev-parse", "HEAD")
	corre("commit", "-q", "--allow-empty", "-m", "merge de la PR")
	merge := corre("rev-parse", "HEAD")

	antes := leerGH
	t.Cleanup(func() { leerGH = antes })
	leerGH = func(context.Context, ...string) ([]byte, error) { return []byte(merge + "\n"), nil }

	if !tieneElMerge(ctx, dir, merge, "https://github.com/x/y/pull/1") {
		t.Fatal("un main con el merge se despliega")
	}
	if tieneElMerge(ctx, dir, viejo, "https://github.com/x/y/pull/1") {
		t.Fatal("un main atrasado (sin el merge) no se despliega")
	}
	if tieneElMerge(ctx, dir, merge, "") {
		t.Fatal("sin PR registrada no se despliega a ciegas")
	}
}

// H2: después de deployMaxIntentos fallos la tarea queda TRABADA y no se reintenta sola.
func TestDeployFallidoSeTraba(t *testing.T) {
	ctx := context.Background()
	_, tk := tareaConRepos(t, DesplieguePendiente, "acme")
	c := Config{Runs: tk.runs}
	for i := 1; i <= deployMaxIntentos; i++ {
		mains := map[string]*mainDeploy{"acme": {err: os.ErrDeadlineExceeded}}
		c.desplegarTarea(ctx, tk, mains)
		got, _ := findTask(c.Runs, tk.Est.ID)
		quiero := DesplieguePendiente
		if i == deployMaxIntentos {
			quiero = Trabada
		}
		if got.Est.Estado != quiero {
			t.Fatalf("intento %d: %s, quiero %s", i, got.Est.Estado, quiero)
		}
		tk = got
	}
}
