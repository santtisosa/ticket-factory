package fab

import (
	"context"
	"strings"
	"testing"
)

func TestMaskTapaDatosPersonales(t *testing.T) {
	in := "fallo para juan.perez@example.com cédula 4.123.456-7 tel 099123456 token=abc123 RUT 211234560019 " +
		"dsn app:Sup3rClave@tcp(10.0.0.1:3306)/erp url mysql://root:otraclave@db:3306/x " +
		"auth Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJh api_key: \"sk-123456\" " +
		"cel +598 99 123 456 y 098-765-432 cédula 1234567-8 access_token=abc123xyz {\"access_token\":\"tok999\"} " +
		"DB_PASSWORD=hunter2 client_secret: s3cr3t password: \"dos palabras\""
	out := Mask(in)
	for _, dato := range []string{"juan.perez", "4.123.456-7", "099123456", "abc123", "211234560019", "Sup3rClave",
		"otraclave", "eyJhbGci", "sk-123456", "99 123 456", "098-765-432", "1234567-8", "abc123xyz", "tok999", "hunter2", "s3cr3t", "dos palabras"} {
		if strings.Contains(out, dato) {
			t.Errorf("quedó %q a la vista: %s", dato, out)
		}
	}
}

// Lo que sirve para diagnosticar queda: montos, ids, "token expirado".
func TestMaskNoTapaMontos(t *testing.T) {
	for _, in := range []string{"total 12345678 no cuadra", "saldo $ 1.234.567,50", "factura 4512 de 2026", "el token expiró", "total 91234567", "id 98765432"} {
		if out := Mask(in); out != in {
			t.Errorf("Mask(%q) = %q", in, out)
		}
	}
}

func TestSensible(t *testing.T) {
	cases := map[string][]string{
		"el total de la factura no cuadra":        {"plata"},
		"el login no guarda la sesión":            {"auth"},
		"cambiar el texto del botón de ayuda":     nil,
		"un cliente ve los cobros de otro tenant": {"plata", "datos"},
		"el cliente no aparece en la lista":       nil,
		"la cuenta activa no se muestra":          nil,
		"invoice total mismatch after refund":     {"plata"},
		"session expired: invalid CSRF token":     {"auth"},
		"falló la migración 0042":                 {"datos"},
	}
	for in, want := range cases {
		got := Sensible(in)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("Sensible(%q) = %v, quería %v", in, got, want)
		}
	}
}

func TestTarjetaDelTablero(t *testing.T) {
	cases := []struct {
		b                   BoardSource
		title, status, want string
		ok                  bool
	}{
		{BoardSource{}, "Fabrica: agregar filtro", "Todo", "agregar filtro", true},
		{BoardSource{}, "fabrica:sin espacio", "Todo", "sin espacio", true},
		{BoardSource{}, "fabrica: ya hecha", "Done", "", false},
		{BoardSource{}, "Revisar a mano", "Todo", "", false},
		{BoardSource{Columna: "Listo para hacer", Prefijo: "[bot]"}, "[BOT] ordenar", "Listo para hacer", "ordenar", true},
		{BoardSource{Columna: "Listo para hacer", Prefijo: "[bot]"}, "[bot] ordenar", "Todo", "", false},
	}
	for _, c := range cases {
		got, ok := c.b.tarjeta(c.title, c.status)
		if ok != c.ok || got != c.want {
			t.Errorf("tarjeta(%q,%q) = %q,%v; quería %q,%v", c.title, c.status, got, ok, c.want, c.ok)
		}
	}
}

// Revisión de seguridad: una tarjeta entra lista solo si es un borrador del tablero o la abrió un
// autor de la lista; un issue de cualquier otro (o sin autor legible) queda en PROPUESTA.
func TestTarjetasListasSoloBorradorOAutor(t *testing.T) {
	b := BoardSource{Autores: []string{"Santi"}}
	out := []byte(`{"items":[
		{"id":"1","title":"fabrica: borrador","status":"Todo","content":{"type":"DraftIssue","body":"b"}},
		{"id":"2","title":"fabrica: de santi","status":"Todo","content":{"type":"Issue","author":{"login":"santi"}}},
		{"id":"3","title":"fabrica: de un extraño","status":"Todo","content":{"type":"Issue","author":{"login":"malo"}}},
		{"id":"4","title":"fabrica: sin autor","status":"Todo","content":{"type":"PullRequest"}},
		{"id":"5","title":"fabrica: autor texto","status":"Todo","content":{"type":"Issue","author":"santi"}}]}`)
	items, err := b.parsear(out)
	if err != nil {
		t.Fatal(err)
	}
	quiero := map[string]bool{"tarjeta:1": true, "tarjeta:2": true, "tarjeta:3": false, "tarjeta:4": false, "tarjeta:5": true}
	if len(items) != len(quiero) {
		t.Fatalf("items: %+v", items)
	}
	for _, it := range items {
		if it.Listo != quiero[it.Clave] {
			t.Errorf("%s: listo=%v", it.Clave, it.Listo)
		}
	}
	if items, _ := (BoardSource{}).parsear(out); items[1].Listo {
		t.Error("sin lista de autores, solo los borradores entran listos")
	}
}

type fakeSource struct{ items []Item }

func (fakeSource) Name() string                            { return "fake" }
func (f fakeSource) Fetch(context.Context) ([]Item, error) { return f.items, nil }

func TestIngestNoDuplicaYUnLogQuedaPropuesta(t *testing.T) {
	runs := t.TempDir()
	src := fakeSource{[]Item{
		{Clave: "tarjeta:1", Fuente: "tarjeta", Texto: "hacer X", Listo: true},
		{Clave: "error:9", Fuente: "log", Texto: "panic para ana@example.com"},
	}}
	Ingest(context.Background(), runs, []Source{src})
	Ingest(context.Background(), runs, []Source{src}) // segunda lectura: nada nuevo
	ts, err := LoadTasks(runs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("quería 2 tareas, hay %d", len(ts))
	}
	estados := map[string]string{}
	for _, tk := range ts {
		estados[tk.Est.Titulo] = tk.Est.Estado
	}
	if estados["hacer X"] != Pendiente {
		t.Errorf("la tarjeta tiene que quedar PENDIENTE: %v", estados)
	}
	if estados["panic para <email>"] != Propuesta {
		t.Errorf("el log tiene que quedar PROPUESTA y enmascarado: %v", estados)
	}
}

func TestGithubSlug(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/demo-api.git": "acme/demo-api",
		"git@github.com:acme/notas.git":        "acme/notas",
	} {
		if got, err := githubSlug(in); err != nil || got != want {
			t.Errorf("githubSlug(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := githubSlug("/Users/x/repo"); err == nil {
		t.Error("un remote local no es de GitHub")
	}
}

type sinConfig struct{}

func (sinConfig) Name() string { return "betterstack" }
func (sinConfig) Fetch(context.Context) ([]Item, error) {
	return nil, errNoConfig{"BETTERSTACK_TOKEN"}
}

// La página lee ingesta.json para mostrar si los tickets llegan: una fuente sin configurar
// queda apagada, no rota, y una que leyó cuenta lo nuevo.
func TestIngestDejaElEstadoDeCadaFuente(t *testing.T) {
	runs := t.TempDir()
	src := fakeSource{[]Item{{Clave: "t:1", Fuente: "ticket", Texto: "arreglar Y", Listo: true}}}
	Ingest(context.Background(), runs, []Source{src, sinConfig{}})
	in := LoadIngesta(runs)
	if in == nil || len(in.Fuentes) != 2 {
		t.Fatalf("quería el estado de 2 fuentes: %+v", in)
	}
	if f := in.Fuentes[0]; !f.OK || f.Nuevas != 1 || f.Leidas != 1 {
		t.Errorf("la fuente que leyó: %+v", f)
	}
	if f := in.Fuentes[1]; f.OK || !f.SinConf {
		t.Errorf("la fuente sin configurar tiene que quedar apagada: %+v", f)
	}
}

func TestPrioridad(t *testing.T) {
	if !(PrioridadDe("log", 0, nil) > PrioridadDe("prompt", 0, nil) && PrioridadDe("prompt", 0, nil) > PrioridadDe("ticket", 0, nil)) {
		t.Fatal("un error de producción va antes que un pedido, y este antes que una sugerencia")
	}
	if PrioridadDe("ticket", 0, []string{"plata"}) <= PrioridadDe("ticket", 0, nil) || PrioridadDe("log", 500, nil) != PrioridadDe("log", 50, nil) {
		t.Fatal("lo sensible sube; las repeticiones suben con tope")
	}
}
