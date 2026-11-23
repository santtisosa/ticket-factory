package fab

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const httpTimeout = 30 * time.Second

// ghLimite: cuántos issues o tarjetas se leen por vuelta (gh trae 30 por defecto).
const ghLimite = "200"

func getJSON(ctx context.Context, c *http.Client, url string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("GET %s: %d %s", url, res.StatusCode, b)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// SourcesDe arma las fuentes declaradas en fabrica.yaml. Lo que no está declarado no se lee.
func SourcesDe(e Espacio) []Source {
	var out []Source
	f := e.Fuentes
	if gi := f.GitHubIssues; gi != nil {
		out = append(out, GitHubIssuesSource{Repo: gi.Repo, Etiqueta: gi.Etiqueta, Autores: gi.Autores, Proyecto: gi.Proyecto})
	}
	if gp := f.GitHubProjects; gp != nil {
		out = append(out, BoardSource{Owner: gp.Owner, Number: strconv.Itoa(gp.Numero), Columna: gp.Columna, Prefijo: gp.Prefijo, Autores: gp.Autores})
	}
	if f.BetterStack != nil {
		out = append(out, BetterStackSource{Token: os.Getenv("BETTERSTACK_TOKEN")})
	}
	for _, x := range f.Exec {
		out = append(out, ExecSource{Nombre: x.Nombre, Comando: x.Comando, Dir: e.Dir})
	}
	return out
}

// SourcesDelEspacio: las fuentes del fabrica.yaml de home; sin fabrica.yaml válido, ninguna.
func SourcesDelEspacio(home string) ([]Source, error) {
	e, err := CargarEspacio(home)
	if err != nil {
		return nil, err
	}
	return SourcesDe(e), nil
}

// ---- GitHub Issues: los issues abiertos con la etiqueta. ----

type GitHubIssuesSource struct {
	Repo, Etiqueta string
	Autores        []string
	Proyecto       string
}

func (GitHubIssuesSource) Name() string { return "github-issues" }

// autorizado: solo un autor de la lista entra directo a la cola. Sin lista, nadie: cualquiera puede
// abrir un issue en un repositorio público y su texto llegaría al Builder.
func (s GitHubIssuesSource) autorizado(login string) bool { return autorEnLista(s.Autores, login) }

func autorEnLista(autores []string, login string) bool {
	for _, a := range autores {
		if login != "" && strings.EqualFold(a, login) {
			return true
		}
	}
	return false
}

func (s GitHubIssuesSource) Fetch(ctx context.Context) ([]Item, error) {
	out, err := leerGH(ctx, "issue", "list", "--repo", s.Repo, "--label", s.Etiqueta, "--state", "open",
		"--limit", ghLimite, "--json", "number,title,body,author,url")
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("gh issue list: respuesta inválida: %w", err)
	}
	items := make([]Item, 0, len(issues))
	for _, is := range issues {
		origen := fmt.Sprintf("%s#%d", s.Repo, is.Number)
		items = append(items, Item{Clave: "issue:" + origen, Fuente: "ticket", Origen: origen, Cliente: s.Proyecto,
			Texto: strings.TrimSpace(is.Title + "\n\n" + is.Body), Listo: s.autorizado(is.Author.Login)})
	}
	return items, nil
}

// ---- GitHub Projects: tarjetas de una columna cuyo título empieza con el prefijo. ----

type BoardSource struct {
	Owner, Number, Columna, Prefijo string
	Autores                         []string
}

func (BoardSource) Name() string { return "github-projects" }

// tarjeta: el título sin el prefijo, si la tarjeta es para la fábrica.
func (b BoardSource) tarjeta(title, status string) (string, bool) {
	t, p := strings.TrimSpace(title), cmp.Or(b.Prefijo, "fabrica:")
	if status != cmp.Or(b.Columna, "Todo") || len(t) < len(p) || !strings.EqualFold(t[:len(p)], p) {
		return "", false
	}
	return strings.TrimSpace(t[len(p):]), true
}

func (b BoardSource) Fetch(ctx context.Context) ([]Item, error) {
	out, err := leerGH(ctx, "project", "item-list", b.Number, "--owner", b.Owner, "--format", "json", "-L", ghLimite)
	if err != nil {
		return nil, fmt.Errorf("gh project item-list: %w", err)
	}
	return b.parsear(out)
}

// parsear: una tarjeta entra lista solo si es un borrador del tablero (lo escribe quien puede editar
// el tablero) o si la abrió un autor de la lista; un issue o una PR de cualquier otro, PROPUESTA.
// ponytail: no está verificado que item-list traiga content.author en todas las versiones de gh; si
// no viene (o viene en otra forma), la tarjeta no es de un autor conocido y queda en PROPUESTA.
func (b BoardSource) parsear(out []byte) ([]Item, error) {
	var res struct {
		Items []struct {
			ID, Title, Status string
			Content           struct {
				Body   string          `json:"body"`
				Type   string          `json:"type"`
				Author json.RawMessage `json:"author"`
			} `json:"content"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	var items []Item
	for _, it := range res.Items {
		if title, ok := b.tarjeta(it.Title, it.Status); ok {
			listo := it.Content.Type == "DraftIssue" || autorEnLista(b.Autores, loginDe(it.Content.Author))
			items = append(items, Item{Clave: "tarjeta:" + it.ID, Fuente: "tarjeta", Texto: strings.TrimSpace(title + "\n\n" + it.Content.Body), Listo: listo})
		}
	}
	return items, nil
}

// loginDe: el login de un autor que llega como {"login": "x"} o como "x"; "" si no se entiende.
func loginDe(raw json.RawMessage) string {
	var obj struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Login != "" {
		return obj.Login
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// ---- exec: un comando que imprime tickets en JSON (docs/fuentes.md). ----

type ExecSource struct{ Nombre, Comando, Dir string }

func (s ExecSource) Name() string { return s.Nombre }

// itemExec es un elemento de la salida del comando.
type itemExec struct {
	Clave    string `json:"clave"`
	Texto    string `json:"texto"`
	Listo    bool   `json:"listo"`
	Servicio string `json:"servicio"`
}

func (s ExecSource) Fetch(ctx context.Context) ([]Item, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", s.Comando)
	cmd.Dir, cmd.Env = s.Dir, cleanEnv() // el comando no hereda los secretos de la fábrica
	cmd.WaitDelay = 5 * time.Second      // un proceso hijo que retiene la salida no traba la ingesta
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %v: %s", s.Nombre, err, tail(strings.TrimSpace(stderr.String())))
	}
	return parsearExec(s.Nombre, out)
}

func parsearExec(nombre string, out []byte) ([]Item, error) {
	var xs []itemExec
	if err := json.Unmarshal(bytes.TrimSpace(out), &xs); err != nil {
		return nil, fmt.Errorf("%s: la salida no es una lista JSON de tickets (ver docs/fuentes.md): %w", nombre, err)
	}
	items := make([]Item, 0, len(xs))
	for i, x := range xs {
		if strings.TrimSpace(x.Clave) == "" || strings.TrimSpace(x.Texto) == "" {
			return nil, fmt.Errorf("%s: el elemento %d no tiene clave o texto", nombre, i)
		}
		items = append(items, Item{Clave: nombre + ":" + x.Clave, Fuente: "ticket", Texto: x.Texto, Listo: x.Listo, Servicio: x.Servicio})
	}
	return items, nil
}

// ---- Better Stack Uptime: incidentes. Siempre PROPUESTA. ----

type bsIncident struct {
	ID         string `json:"id"`
	Attributes struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		Cause     string `json:"cause"`
		StartedAt string `json:"started_at"`
	} `json:"attributes"`
}

type BetterStackSource struct{ Token string }

func (BetterStackSource) Name() string { return "betterstack" }

func (s BetterStackSource) Fetch(ctx context.Context) ([]Item, error) {
	if s.Token == "" {
		return nil, errNoConfig{"BETTERSTACK_TOKEN"}
	}
	// Los abiertos (aunque hayan empezado hace días) más los que arrancaron en las últimas 24 h.
	from := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	hdr := map[string]string{"Authorization": "Bearer " + s.Token}
	seen := map[string]bool{}
	var data []bsIncident
	for _, q := range []string{"resolved=false", "from=" + from} {
		var page struct {
			Data []bsIncident `json:"data"`
		}
		if err := getJSON(ctx, &http.Client{Timeout: httpTimeout}, "https://uptime.betterstack.com/api/v2/incidents?"+q, hdr, &page); err != nil {
			return nil, err
		}
		for _, d := range page.Data {
			if !seen[d.ID] {
				seen[d.ID] = true
				data = append(data, d)
			}
		}
	}
	var items []Item
	for _, d := range data {
		a := d.Attributes
		items = append(items, Item{Clave: "incidente:" + d.ID, Fuente: "log", Servicio: a.Name,
			Texto: fmt.Sprintf("Incidente de Better Stack en %s (%s) desde %s: %s", a.Name, a.URL, a.StartedAt, a.Cause)})
	}
	return items, nil
}
