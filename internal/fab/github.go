package fab

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// Todo lo que la fábrica hace contra GitHub (leer fuentes, etiquetar issues, publicar la rama, abrir y
// seguir la PR, el merge automático, el despliegue y los comentarios) usa un token propio de la fábrica,
// FABRICA_GITHUB_TOKEN, y nunca las credenciales del usuario (llavero, gh auth, ssh). El token se saca
// del entorno del proceso al arrancar: ningún hijo lo hereda por os.Environ(). Solo lo reciben los exec
// de gh (como GH_TOKEN) y de git que hablan con GitHub (entornoGH, comandoGitGitHub). Nunca los agentes,
// el chequeo ni nada que corra en srt.

// VarTokenGitHub es la variable del token de la fábrica (en el entorno o en ~/.config/ticket-factory/env).
const VarTokenGitHub = "FABRICA_GITHUB_TOKEN"

// errSinTokenGitHub: la primera operación contra GitHub sin token falla con esto.
var errSinTokenGitHub = errors.New(VarTokenGitHub + " no está definido; cree un token fine-grained limitado a los repositorios de fabrica.yaml (ver README, sección Seguridad)")

// tokenGitHub es el token de la fábrica, fuera del entorno del proceso.
var tokenGitHub string

// guardarTokenGitHub mueve el token del entorno a tokenGitHub. Lo llama NewConfig después de leer el
// archivo env.
func guardarTokenGitHub() {
	if v := os.Getenv(VarTokenGitHub); v != "" {
		tokenGitHub = v
	}
	_ = os.Unsetenv(VarTokenGitHub)
}

// EntornoConToken: el entorno del proceso más el token, para relanzar la fábrica misma (la consola que
// arranca `fabrica run`, la línea que se re-ejecuta con un binario nuevo). Nada más lo usa.
func EntornoConToken() []string {
	if tokenGitHub == "" {
		return os.Environ()
	}
	return append(os.Environ(), VarTokenGitHub+"="+tokenGitHub)
}

func tokenDeGitHub() (string, error) {
	if tokenGitHub == "" {
		return "", errSinTokenGitHub
	}
	return tokenGitHub, nil
}

// dirConfigGH: la configuración de gh de la fábrica (vacía): gh no lee el hosts.yml del usuario.
func dirConfigGH() string {
	if raizRuns == "" {
		return filepath.Join(os.TempDir(), "fabrica-gh")
	}
	return filepath.Join(raizRuns, ".cache", "gh")
}

// entornoGH: el entorno de un gh de la fábrica. Sin token, error.
func entornoGH() ([]string, error) {
	tok, err := tokenDeGitHub()
	if err != nil {
		return nil, err
	}
	dir := dirConfigGH()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return append(cleanEnv(), "GH_TOKEN="+tok, "GH_CONFIG_DIR="+dir, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1"), nil
}

// comandoGH es el único camino por el que la fábrica ejecuta gh.
func comandoGH(ctx context.Context, args ...string) (*exec.Cmd, error) {
	env, err := entornoGH()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = env
	return cmd, nil
}

// ghSalida: stdout de gh.
func ghSalida(ctx context.Context, args ...string) ([]byte, error) {
	cmd, err := comandoGH(ctx, args...)
	if err != nil {
		return nil, err
	}
	return cmd.Output()
}

// ghCombinada: stdout y stderr de gh.
func ghCombinada(ctx context.Context, args ...string) ([]byte, error) {
	cmd, err := comandoGH(ctx, args...)
	if err != nil {
		return nil, err
	}
	return cmd.CombinedOutput()
}

// helperToken: el único credential helper de los git que hablan con GitHub. Lee el token de la
// variable del entorno de ese exec (forma documentada en gitfaq, "How do I read a password or token
// from an environment variable?").
const helperToken = `!f() { test "$1" = get && echo username=x-access-token && echo "password=$` + VarTokenGitHub + `"; }; f`

// urlGitHub: https://github.com/<owner>/<repo>.git para un remote https o ssh de GitHub. Siempre https:
// con ssh git usaría las llaves del usuario.
func urlGitHub(remote string) (string, error) {
	slug, err := githubSlug(remote)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + slug + ".git", nil
}

// esRemotoGitHub: un remote https o ssh de GitHub (no una carpeta local que tenga github.com en la ruta).
func esRemotoGitHub(remote string) bool {
	_, err := githubSlug(remote)
	return err == nil
}

// comandoGitGitHub: git contra GitHub sobre dir ("" = sin repo, para un clone). Sin la config global ni
// la del sistema (ni helpers ni insteadOf del usuario), con el blindaje de siempre, sin prompts y con el
// token como único helper. Sin token, error.
func comandoGitGitHub(ctx context.Context, dir string, args ...string) (*exec.Cmd, error) {
	tok, err := tokenDeGitHub()
	if err != nil {
		return nil, err
	}
	var env []string
	if dir != "" {
		if env, err = entornoGitSeguro(dir); err != nil {
			return nil, err
		}
	} else {
		env, _ = entornoGitSeguro(os.TempDir())
	}
	pre := []string{"-c", "credential.helper=", "-c", "credential.helper=" + helperToken}
	if dir != "" {
		pre = append(pre, "-C", dir)
	}
	cmd := exec.CommandContext(ctx, "git", append(pre, args...)...)
	cmd.Env = conEntorno(cleanEnv(), append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/usr/bin/false", "SSH_ASKPASS=/usr/bin/false", VarTokenGitHub+"="+tok))
	return cmd, nil
}
