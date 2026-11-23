package fab

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// El cerebro de la fábrica es un clon privado del repo de conocimiento (conocimiento.repo de
// fabrica.yaml) en la rama `cerebro`: el Bibliotecario escribe ahí y el Jefe lee de ahí. El repo
// original no se toca; llevar lo aprendido a él es `fabrica cerebro-sync`, con confirmación.
const brainBranch = "cerebro"

// ErrSinConocimiento: fabrica.yaml no declara conocimiento. El Jefe y el Bibliotecario no corren.
var ErrSinConocimiento = errors.New("fabrica.yaml no declara conocimiento.repo: el Jefe y el Bibliotecario no están disponibles")

// conocimiento devuelve la sección conocimiento de fabrica.yaml, o ErrSinConocimiento.
func (c Config) conocimiento() (Conocimiento, error) {
	e, err := CargarEspacio(c.Home)
	if err != nil {
		if errors.Is(err, ErrSinEspacio) {
			return Conocimiento{}, ErrSinConocimiento
		}
		return Conocimiento{}, err
	}
	if e.Conocimiento == nil {
		return Conocimiento{}, ErrSinConocimiento
	}
	return *e.Conocimiento, nil
}

// conConocimiento: fabrica.yaml declara un repo de conocimiento válido.
func (c Config) conConocimiento() bool {
	_, err := c.conocimiento()
	return err == nil
}

func (c Config) BrainClone() string { return filepath.Join(c.Home, "clones", "conocimiento") }

// carpetaCerebro: el encabezado del prompt del Jefe y del Bibliotecario. Su cwd no es la carpeta del
// cerebro (C3): las rutas relativas que usan son relativas a ella.
func carpetaCerebro(farm string) string {
	return "La carpeta del cerebro es " + farm + ". Las rutas relativas de este pedido y de las citas son relativas a ella; " +
		"para leer, usa la ruta absoluta.\n\n"
}

// BrainFarm es la carpeta que ve el Jefe: indice.md y un enlace al clon (conocimiento/).
func (c Config) BrainFarm() string { return filepath.Join(c.Runs, ".cache", "cerebro") }

// EnsureBrain clona el repo de conocimiento (una vez), trae su main y rebasa `cerebro` encima. Si el
// rebase choca, lo aborta y sigue con lo que había: lo aprendido no se pierde.
func (c Config) EnsureBrain(ctx context.Context) error {
	k, err := c.conocimiento()
	if err != nil {
		return err
	}
	clone := c.BrainClone()
	if err := ensureClone(ctx, k.Repo, clone); err != nil {
		return err
	}
	if _, err := git(ctx, clone, "rev-parse", "--verify", brainBranch); err != nil {
		if _, err := git(ctx, clone, "checkout", "--quiet", "-b", brainBranch, Base); err != nil {
			return err
		}
	} else if _, err := git(ctx, clone, "checkout", "--quiet", brainBranch); err != nil {
		return err
	}
	if _, err := git(ctx, clone, "rebase", "--quiet", Base); err != nil {
		_, _ = git(ctx, clone, "rebase", "--abort")
		fmt.Fprintln(os.Stderr, "cerebro: el rebase sobre el repo de conocimiento chocó; se sigue con la rama como estaba:", err)
		c.avisarChoque(err)
	}
	return c.buildFarm()
}

func (c Config) buildFarm() error {
	farm := c.BrainFarm()
	if err := os.MkdirAll(farm, 0o755); err != nil {
		return err
	}
	link, target := filepath.Join(farm, "conocimiento"), c.BrainClone()
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil
	}
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("enlazar conocimiento: %w", err)
	}
	return nil
}

var reHeading = regexp.MustCompile(`^#\s+(.+)`)

// BuildIndex escribe indice.md en la carpeta del Jefe: un renglón por nota del repo de conocimiento.
// Con unas cientos de notas alcanza, sin embeddings.
func (c Config) BuildIndex() (string, error) {
	farm := c.BrainFarm()
	var b strings.Builder
	b.WriteString("# Índice del cerebro\n\nRutas relativas a esta carpeta. Generado por `fabrica indice`.\n\n## Conocimiento\n\n")
	// WalkDir no sigue el symlink de la carpeta del Jefe: se recorre el clon y se reescribe la ruta.
	clone := c.BrainClone()
	var notes []string
	_ = filepath.WalkDir(clone, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(clone, p)
			rel = filepath.Join("conocimiento", rel)
			notes = append(notes, fmt.Sprintf("- `%s` — %s", rel, firstHeading(p)))
		}
		return nil
	})
	sort.Strings(notes)
	b.WriteString(strings.Join(notes, "\n") + "\n")
	out := filepath.Join(farm, "indice.md")
	return out, os.WriteFile(out, []byte(b.String()), 0o644)
}

func firstHeading(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := reHeading.FindStringSubmatch(sc.Text()); m != nil {
			return m[1]
		}
	}
	return ""
}

// avisarChoque deja una tarjeta en el tablero (una por día): si nadie mira el rebase que choca, el
// cerebro se queda congelado contra un repo de conocimiento que sigue cambiando.
func (c Config) avisarChoque(err error) {
	clave := "cerebro-rebase:" + time.Now().Format("2006-01-02")
	if visto(c.Runs, clave) {
		return
	}
	texto := "El cerebro de la fábrica no se pudo rebasar sobre el main del repo de conocimiento: lo aprendido choca con algo " +
		"que cambió en el repo. Hay que resolverlo a mano en clones/conocimiento (rama cerebro) o descartar lo aprendido.\n\n" + tail(err.Error())
	if _, e := AddTask(c.Runs, texto, "cerebro", Propuesta, ""); e == nil {
		_ = marcarVisto(c.Runs, clave)
	}
}
