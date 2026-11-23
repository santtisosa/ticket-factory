package fab

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RepoCliente es un repo de un proyecto de fabrica.yaml, con la ruta ya resuelta.
type RepoCliente struct {
	Nombre   string
	Ruta     string // URL de git o carpeta: de ahí sale el clon privado
	Front    bool   // tiene pantallas: ahí miran UX y UI
	Check    string
	CIManual bool
	Deploy   string
	Smoke    string
}

// Cliente es un proyecto de fabrica.yaml: los repos que una tarea puede tocar juntos.
type Cliente struct {
	Slug  string
	Repos []RepoCliente
}

// Clientes arma la tabla de proyectos de fabrica.yaml. Define a qué repos puede ir una tarea. Sin
// fabrica.yaml (o con uno inválido) no hay proyectos: NewConfig ya avisó al arrancar.
func Clientes(home string) []Cliente {
	e, err := CargarEspacio(home)
	if err != nil {
		return nil
	}
	cs := make([]Cliente, 0, len(e.Proyectos))
	for _, slug := range sortedKeys(e.Proyectos) {
		c := Cliente{Slug: slug}
		for _, r := range e.Proyectos[slug].Repos {
			c.Repos = append(c.Repos, RepoCliente{Nombre: r.Nombre, Ruta: e.rutaRepo(r.Ruta), Front: r.Frontend,
				Check: r.Check, CIManual: r.CIManual, Deploy: r.Deploy, Smoke: r.Smoke})
		}
		cs = append(cs, c)
	}
	return cs
}

// buscarRepo devuelve el repo y el slug del cliente al que pertenece.
func buscarRepo(cs []Cliente, nombre string) (RepoCliente, string, bool) {
	for _, c := range cs {
		for _, r := range c.Repos {
			if r.Nombre == nombre {
				return r, c.Slug, true
			}
		}
	}
	return RepoCliente{}, "", false
}

func buscarCliente(cs []Cliente, slug string) (Cliente, bool) {
	for _, c := range cs {
		if c.Slug == slug {
			return c, true
		}
	}
	return Cliente{}, false
}

// EsCliente: el slug es un proyecto de fabrica.yaml.
func EsCliente(home, slug string) bool {
	_, ok := buscarCliente(Clientes(home), slug)
	return ok
}

// ClienteDeServicio traduce el nombre de un servicio (o el origen de un ticket) a un proyecto:
// "demo-api" → "demo". Vacío si no es de ninguno.
func ClienteDeServicio(cs []Cliente, servicio string) string {
	s := strings.ToLower(strings.TrimSpace(servicio))
	for _, c := range cs {
		if s == c.Slug || strings.HasPrefix(s, c.Slug+"-") {
			return c.Slug
		}
	}
	return ""
}

func reposDe(c Cliente) []string {
	rs := make([]string, 0, len(c.Repos))
	for _, r := range c.Repos {
		rs = append(rs, r.Nombre)
	}
	sort.Strings(rs)
	return rs
}

func todosLosRepos(cs []Cliente) []string {
	var rs []string
	for _, c := range cs {
		rs = append(rs, reposDe(c)...)
	}
	sort.Strings(rs)
	return rs
}

// describirClientes es la lista que ve el Spec writer para elegir repos.
func describirClientes(cs []Cliente) string {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "- %s: %s\n", c.Slug, strings.Join(reposDe(c), ", "))
	}
	return b.String()
}

// specSchema es el esquema de la spec con el enum de repos armado en el momento: solo los que se
// pueden elegir (los del cliente de la entrada, o todos si no se sabe).
func specSchema(repos []string) []byte {
	var sch map[string]any
	if err := json.Unmarshal(schema("spec.json"), &sch); err != nil {
		panic(err) // embebido: si no se lee es un error del repo
	}
	items := sch["properties"].(map[string]any)["repos"].(map[string]any)["items"].(map[string]any)
	items["enum"] = repos
	b, err := json.Marshal(sch)
	if err != nil {
		panic(err)
	}
	return b
}

// validateSpec controla lo que el esquema no puede: repos de un solo cliente (el de la entrada si
// se sabe), criterios CA-1..n sin huecos, y un motivo si frena.
func validateSpec(s Spec, cliente string, cs []Cliente) error {
	if s.Frenar {
		if strings.TrimSpace(s.MotivoFrenar) == "" {
			return fmt.Errorf("frenó sin motivo")
		}
		return nil
	}
	if len(s.Repos) == 0 {
		return fmt.Errorf("spec sin repos")
	}
	del := ""
	for _, r := range s.Repos {
		_, slug, ok := buscarRepo(cs, r)
		if !ok {
			return fmt.Errorf("el repo %q no es de ningún proyecto", r)
		}
		if del != "" && slug != del {
			return fmt.Errorf("la spec mezcla proyectos (%s y %s): una tarea es de un solo proyecto", del, slug)
		}
		del = slug
	}
	if cliente != "" && del != cliente {
		return fmt.Errorf("la entrada es de %s pero la spec va a %s", cliente, del)
	}
	if len(s.Criterios) == 0 {
		return fmt.Errorf("spec sin criterios")
	}
	for i, c := range s.Criterios {
		if c.ID != fmt.Sprintf("CA-%d", i+1) {
			return fmt.Errorf("los criterios van CA-1..CA-n sin huecos: el %d° es %q", i+1, c.ID)
		}
	}
	return nil
}
