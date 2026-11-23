package fab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// touchesUI: la tarea cambia pantallas (un repo con `frontend: true` y rutas en la spec).
func (c Config) touchesUI(s Spec) bool {
	if len(s.Rutas) == 0 {
		return false
	}
	cs := Clientes(c.Home)
	return slices.ContainsFunc(s.Repos, func(n string) bool { r, _, ok := buscarRepo(cs, n); return ok && r.Front })
}

// maxCriteriosChica: hasta cuántos criterios una tarea sigue siendo chica.
const maxCriteriosChica = 6

// tareaChica: un repo, pocos criterios, un bug o un ajuste. En esas revisan solo código y seguridad:
// levantar la app para UX y UI era otra fuente de trabas por el entorno para un cambio chico.
func tareaChica(s Spec) bool {
	return len(s.Repos) == 1 && len(s.Criterios) <= maxCriteriosChica && (s.Tipo == "bug" || s.Tipo == "ajuste")
}

// infraPrefijo marca un "no entregó" que no es culpa del Builder (la app no se puede levantar para la
// revisión): no vuelve como feedback ni frena; queda "sin revisar" a la vista.
const infraPrefijo = "infra: "

func allOK(cs []Check) bool {
	for _, c := range cs {
		if !c.OK {
			return false
		}
	}
	return true
}

// uiReviewers levanta la app con la receta `preview` del proyecto, corre lo determinista (capturas,
// árbol, axe) y suma UX y UI a los revisores. Sin receta, UX y UI quedan "sin revisar" y no frenan. Si
// la app de la rama no levanta, UX y UI entran como "no entregó" y la vuelta no pasa.
func (c Config) uiReviewers(ctx context.Context, t *Task, spec Spec, repos []Repo, v int, revs []reviewer) (*App, []reviewer, error) {
	conError := func(msg string) []reviewer {
		return append(revs, reviewer{name: "ux", setupErr: msg}, reviewer{name: "ui", setupErr: msg})
	}
	cl, receta, err := c.recetaDe(spec.Repos)
	if err != nil {
		return nil, conError(infraPrefijo + "UX y UI sin revisar: " + err.Error()), nil
	}
	var env []string
	var perfil string
	if len(repos) > 0 {
		env, perfil = repos[0].Env, repos[0].Encierro // el perfil cubre la carpeta de worktrees, hermanos incluidos
	}
	todos := append(slices.Clone(repos), c.hermanos(ctx, t, repos)...)
	bajarModulos(ctx, perfil, dirsOf(todos)...)
	t.Log("04-app", "inicio", "receta preview del proyecto", 0)
	app, err := arrancarApp(ctx, *receta, repoApp(cl, todos).WT, env, filepath.Join(t.Dir, fmt.Sprintf("app-v%d.log", v)), perfil)
	if err != nil {
		return nil, conError(err.Error()), err
	}
	t.Log("04-app", "ok", app.URL, 0)
	outDir := filepath.Join(t.Dir, fmt.Sprintf("ui-v%d", v))
	if _, err := app.Scan(ctx, c.Factory, outDir, spec.Rutas); err != nil {
		app.Stop()
		return nil, conError(err.Error()), err
	}
	artifacts := fmt.Sprintf("\n\n## Lo que ya midieron las herramientas\nEn `%s`: `resumen.json`, una captura `.png` por ruta en escritorio y celular, "+
		"y en escritorio el árbol de accesibilidad (`.aria.yml`) y las violaciones de axe (`.axe.json`).\n"+
		"La app está levantada en %s. Rutas de esta tarea: %v\n", outDir, app.URL, spec.Rutas)
	// Solo UX navega la app (Playwright): solo su prompt lleva el usuario de prueba. El MCP es de la
	// vuelta: solo el origen de la app y la salida dentro de la tarea.
	mcp := filepath.Join(t.Dir, fmt.Sprintf("mcp-playwright-v%d.json", v))
	b, err := mcpPlaywright(filepath.Join(c.Runs, ".cache"), app.URL, filepath.Join(outDir, "playwright"))
	if err == nil {
		err = os.WriteFile(mcp, b, 0o644)
	}
	if err != nil {
		app.Stop()
		return nil, conError(infraPrefijo + "UX sin MCP: " + err.Error()), err
	}
	guia := c.disenoMD()
	ux := reviewer{name: "ux", mcp: mcp, extra: artifacts + "Acceso: " + acceso() + ".\n" + guia, tools: herramientasUX()}
	ui := reviewer{name: "ui", extra: artifacts + guia}
	return app, append(revs, ux, ui), nil
}

// playwrightPermitidas: lo único del MCP de Playwright que usa el revisor de UX (C2): navegar, mirar y
// leer la consola y la red (nombres verificados en el README de @playwright/mcp 0.0.83).
var playwrightPermitidas = []string{"browser_navigate", "browser_navigate_back", "browser_snapshot", "browser_take_screenshot",
	"browser_click", "browser_type", "browser_press_key", "browser_hover", "browser_select_option", "browser_fill_form",
	"browser_handle_dialog", "browser_wait_for", "browser_resize", "browser_console_messages", "browser_network_requests",
	"browser_close", "browser_tabs"}

// playwrightNegadas: las que ejecutan código en la página o en Playwright, suben archivos, leen o
// escriben archivos o estado del navegador. Con dontAsk ya se niegan por no estar en la lista; el deny
// explícito queda por si cambia el modo.
var playwrightNegadas = []string{"browser_run_code_unsafe", "browser_evaluate", "browser_file_upload", "browser_pdf_save",
	"browser_storage_state", "browser_set_storage_state", "browser_route", "browser_unroute", "browser_network_state_set",
	"browser_start_tracing", "browser_stop_tracing", "browser_start_video", "browser_stop_video",
	"browser_start_recording", "browser_stop_recording", "browser_cookie_set", "browser_localstorage_set", "browser_sessionstorage_set"}

// herramientasUX: las de solo lectura más las de Playwright permitidas, una por una.
func herramientasUX() []string {
	ts := slices.Clone(readOnlyTools)
	for _, t := range playwrightPermitidas {
		ts = append(ts, "mcp__playwright__"+t)
	}
	return ts
}

// acceso: cómo entra el revisor a la app. El usuario de prueba sale del entorno de la fábrica, nunca
// del código; sin él, se revisa lo que se ve sin iniciar sesión.
func acceso() string {
	u, k := usuarioDeApp()
	if u == "" {
		return "sin usuario de prueba: se revisa lo que se ve sin iniciar sesión"
	}
	return fmt.Sprintf("usuario de prueba %s, clave %s", u, k)
}

// ProbarApp levanta la app de un proyecto desde main en una carpeta descartable, escanea `/` y la
// baja. Sirve para diagnosticar la receta `preview` sin gastar en agentes. Sin proyecto, usa el único
// que declara `preview`.
func ProbarApp(ctx context.Context, c Config, proyecto string) error {
	if proyecto == "" {
		var err error
		if proyecto, err = proyectoConPreview(c.Home); err != nil {
			return err
		}
	}
	cl, receta, err := c.recetaDeProyecto(proyecto)
	if err != nil {
		return err
	}
	dir := filepath.Join(c.Runs, ".probar-app", proyecto)
	var repos []Repo
	for _, rc := range cl.Repos {
		r := Repo{Name: rc.Nombre, Src: filepath.Join(c.Home, "clones", rc.Nombre), Check: rc.Check}
		if err := AddBaseWorktree(ctx, &r, rc.Ruta, dir); err != nil {
			return fmt.Errorf("worktree de %s: %w", rc.Nombre, err)
		}
		repos = append(repos, r)
	}
	perfil, err := c.prepararEncierro(ctx, "probar-"+proyecto, dir, repos)
	if err != nil {
		return err
	}
	env, err := c.asegurarServicios(ctx, []string{cl.Repos[0].Nombre})
	if err != nil {
		return fmt.Errorf("servicios de prueba: %w", err)
	}
	for i := range repos {
		repos[i].Env, repos[i].Encierro = env, perfil
	}
	front := repoApp(cl, repos)
	if ok, out := RunCheck(ctx, front); !ok {
		fmt.Printf("aviso: el chequeo de %s no pasó en main (se continúa igual): %s\n", front.Name, out)
	}
	app, err := arrancarApp(ctx, *receta, front.WT, env, filepath.Join(dir, "app.log"), perfil)
	if err != nil {
		return err
	}
	defer app.Stop()
	fmt.Println("app levantada en", app.URL)
	out, err := app.Scan(ctx, c.Factory, filepath.Join(dir, "scan"), []string{"/"})
	fmt.Println(out)
	return err
}

// proyectoConPreview: el único proyecto de fabrica.yaml que declara `preview`.
func proyectoConPreview(home string) (string, error) {
	e, err := CargarEspacio(home)
	if err != nil {
		return "", err
	}
	var con []string
	for _, p := range sortedKeys(e.Proyectos) {
		if e.Proyectos[p].Preview != nil {
			con = append(con, p)
		}
	}
	switch len(con) {
	case 0:
		return "", fmt.Errorf("ningún proyecto declara preview en %s", ArchivoEspacio)
	case 1:
		return con[0], nil
	}
	return "", fmt.Errorf("hay varios proyectos con preview (%s): indique uno con fabrica probar-app <proyecto>", strings.Join(con, ", "))
}
