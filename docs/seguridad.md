# Modelo de seguridad

La fábrica ejecuta agentes de Claude Code y el código que esos agentes escriben, con el mismo usuario de macOS que la
opera. El riesgo principal es que ese código, o un agente inducido por el texto de un ticket, publique en GitHub sin
aprobación humana, lea credenciales del usuario o deje algo que la fábrica ejecute después fuera de todo encierro.

Decisión del dueño (22/11): se mantiene el mismo usuario de macOS, endurecido, y todo lo que habla con GitHub usa un
token propio de la fábrica en lugar de las credenciales del usuario.

## Qué corre dónde

| Proceso | Encierro | Red | Credenciales que tiene |
|---|---|---|---|
| Spec writer y revisores de seguridad, código y UI (su Bash) | Sandbox de Claude Code por tarea (`runs/<id>/settings-spec.json` y `settings-lector.json`, `failIfUnavailable`, sin `autoAllowBashIfSandboxed`): Bash solo con los comandos de lectura en lista, sin escribir en sus `--add-dir` (worktrees, repos de origen, cerebro) ni en los clones; solo en el temporal de la tarea. `fabrica preguntar` del Spec writer corre fuera del sandbox (`excludedCommands`): llama al Jefe. | Ninguna desde Bash (`allowedDomains` vacío, sin `localhost`). Sin WebFetch ni WebSearch. | La autenticación de Claude Code del usuario (la necesita el proceso). La herramienta Read tiene negadas las credenciales, el llavero, `~/.claude` (salvo la carpeta de sesiones del cwd de los agentes), `~/Library/Application Support`, `~/Library/Cookies` y cada entrada de `~/Documents` que no contiene al espacio ni a la fábrica; el Bash, lo mismo por `denyRead`. |
| Jefe, Bibliotecario, Notificación (`claude -p`) | Ninguno a nivel de sistema operativo: no tienen Bash. Los limitan los permisos (`--permission-mode dontAsk`, `--allowedTools`, `deny`). | La de Claude Code hacia Anthropic. | Las mismas que la herramienta Read de arriba. |
| Revisor de UX | Su Bash, como los revisores. El servidor MCP de Playwright corre **fuera de todo sandbox** (`npx`, Node y Chromium como el usuario). | El navegador, solo al origen de la app de la tarea (`--allowed-origins`; Playwright aclara que no es una frontera de seguridad ni afecta redirecciones). | Las mismas. |
| Builder (su Bash) | Sandbox de Claude Code, por tarea (`runs/<id>/settings-builder.json`, `failIfUnavailable`). Edit y Write no pasan por el sandbox: en `dontAsk` se niega todo lo que no esté en `allow`, y el único `allow` de Edit es `Edit(//<worktree>/**)` de cada repo de la tarea (sin `Edit` ni `Write` sueltos). Los `deny` siguen como segunda capa. | `registry.npmjs.org`, `proxy.golang.org`, `sum.golang.org` y `localhost`. Sin trustd: el Go del sandbox no verifica TLS, así que los módulos los baja la fábrica antes. | Ninguna legible: `denyRead` de credenciales, llavero, `~/Documents` (con `allowRead` del espacio y de la fábrica), `~/.claude`, `Application Support` y `Cookies`. |
| Revisor del resultado (su Bash) | El mismo sandbox (`settings-resultado.json`), sin escribir en los clones. | Igual que el Builder. | Igual que el Builder. |
| Chequeo (`RunCheck`), hook Stop del Builder, vista previa y app de UX/UI | `srt`, perfil de código `runs/.cache/srt-<tarea>.json`. | Los tres registros de dependencias y `localhost`. | Ninguna legible (ver el perfil). |
| Escaneo determinista (gosec, govulncheck, semgrep, gitleaks, `npm audit`) y descarga de módulos de Go | `srt`, perfil de herramientas `runs/.cache/srt-<tarea>-herramientas.json`. Ninguna de estas herramientas ejecuta código de la rama. | Los registros más `vuln.go.dev`, `semgrep.dev` y `storage.googleapis.com` (adonde `proxy.golang.org` redirige los zips grandes; sin él, `modernc.org/sqlite` no baja, probado); trustd habilitado (sin él Go no verifica TLS, probado); sin `localhost`. | Ninguna legible. |
| `ui-scan` (capturas, árbol de accesibilidad, axe) | Ninguno: Node y Playwright de la fábrica, con el entorno limpio. Navega la app de la rama. | La de Node. | Solo el usuario de prueba de la app, si está definido. |
| Servicios de prueba (`servicios` de `fabrica.yaml`) | Docker, lanzado por la fábrica. | Puerto publicado en `127.0.0.1`. | Las del contenedor. |
| La fábrica: git, gh, aprobación, seguimiento, merge automático | Ninguno: proceso de la fábrica. | GitHub. | Solo `FABRICA_GITHUB_TOKEN` (ver abajo). |
| Despliegue (`deploy` de `fabrica.yaml`) | Ninguno. Corre sobre el `main` de GitHub ya integrado, exportado a una carpeta temporal de la fábrica. | La que use el comando. | **Las del usuario** (por ejemplo, `gcloud`): el comando del despliegue las necesita. Sin el token de GitHub. |
| Fuentes `exec` y notificación por comando | Ninguno: comandos que escribió el operador en `fabrica.yaml`. | La que usen. | Entorno limpio (sin los secretos de la fábrica). |

Los hooks de Claude Code no pasan por su sandbox. Por eso el hook Stop no ejecuta `$FABRICA_CHECK` directamente:
ejecuta `$FABRICA_CHECK_ENCERRADO`, la misma línea envuelta en `srt`. Sin esa variable no ejecuta nada y el chequeo de
la fábrica decide. El Builder ejecuta `$FABRICA_CHECK` desde su Bash, que ya está dentro del sandbox de Claude Code.

## GitHub: el token de la fábrica

Todo lo que la fábrica hace contra GitHub usa `FABRICA_GITHUB_TOKEN` (del entorno o de `~/.config/ticket-factory/env`):
leer las fuentes, etiquetar issues, comentar, publicar la rama, abrir y seguir la PR, relanzar el CI, el merge
automático, el clon del despliegue y el `fetch` de `main`. Al arrancar, la fábrica saca el token de su propio entorno:
ningún proceso hijo lo hereda. Solo lo reciben:

- **gh**, como `GH_TOKEN`, con `GH_CONFIG_DIR` en una carpeta vacía de la fábrica (`runs/.cache/gh`): no lee el
  `hosts.yml` del usuario.
- **git contra GitHub**, siempre por `https://github.com/<owner>/<repo>.git` (un remoto SSH se reescribe), sin la
  configuración global ni la del sistema (ni helpers ni `insteadOf` del usuario), con `credential.helper` vaciado y un
  único helper que devuelve el token desde la variable de ese proceso (la forma que documenta el gitfaq de git).
  Probado: con un helper señuelo en la configuración global y otro con ámbito `credential.https://github.com` en la del
  repo, `git credential fill` devuelve solo el token.

Nunca llega a los agentes, al chequeo ni a nada que corra en `srt`. Sin token, la primera operación contra GitHub falla
con `FABRICA_GITHUB_TOKEN no está definido; cree un token fine-grained limitado a los repositorios de fabrica.yaml`.

Permisos mínimos del token (fine-grained, acceso solo a los repositorios de `fabrica.yaml`). Contra la documentación
de GitHub se verificaron Actions (disparar un workflow pide escritura) y Workflows (solo existe en escritura); el resto
sale de lo que hace cada comando y no se probó con un token real:

| Permiso | Nivel | Para qué |
|---|---|---|
| Metadata | Lectura | Obligatorio. |
| Contents | Lectura y escritura | `fetch`, clon y push de la rama de la tarea; el merge. |
| Pull requests | Lectura y escritura | Crear, pasar a lista, leer y mergear la PR. |
| Issues | Lectura y escritura | Fuente `github-issues`, etiquetas de seguimiento y comentarios de notificación. |
| Actions | Lectura y escritura | Leer las corridas, relanzar los jobs fallidos y, con `ci_manual`, disparar `ci.yml` (verificado). |
| Commit statuses | Lectura | El estado de los checks de la PR. |
| Workflows | No otorgar | Sin él, GitHub rechaza el push de una rama que cambia `.github/workflows`: esas tareas las publica una persona. |
| Projects (organización) | Lectura | Solo con la fuente `github-projects`. |

Se recomienda proteger `main` con una regla (ruleset) que exija pull request y el check requerido (`ci-ok`) sin lista de
excepciones: así el token no puede integrar nada que el CI no haya aprobado, aunque la cuenta del token sea
administradora del repositorio.

## El cwd de los agentes

Con `--setting-sources local`, Claude Code carga `<cwd>/.claude/settings.local.json` y, si el cwd está dentro de un
repo git, el de su raíz; con `-p`, sus hooks se ejecutan sin pedir confianza. Por eso todo `claude -p` corre con cwd en
`~/Library/Caches/ticket-factory/agentes-<hash del espacio>`: fuera de todo repo, sin escritura para `srt` ni para los
agentes (`denyWrite` y `deny` de `Edit`). `Run` verifica antes de cada llamada que no esté dentro de un repo git y que
no tenga `.claude`, `CLAUDE.md`, `CLAUDE.local.md` ni `.mcp.json`; si no se cumple, no arranca. Los worktrees, los repos
y el cerebro llegan solo por `--add-dir`.

Según la documentación de Claude Code (permisos, "Additional directories grant file access, not configuration"), un
directorio de `--add-dir` no aporta settings ni hooks: sus skills, comandos y subagentes se cargan por la fuente
`project`, que la fábrica no usa; de sus `settings*.json` solo se leen `enabledPlugins` y `extraKnownMarketplaces`, y
solo con las fuentes `project` y `local`; sus `CLAUDE.md` solo con `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`, que
no llega a los agentes (lista blanca de entorno). Esto está confirmado por la documentación, no probado con `claude`.
Además, `.claude/` y `CLAUDE.local.md` de los worktrees no se pueden escribir desde `srt` ni con Edit.

## Los perfiles de `srt`

Los genera `perfilEncierro` (`internal/fab/encierro.go`), única fuente de verdad, por tarea:

- **Escritura:** la carpeta de worktrees de la tarea (`runs/<id>/wt`), el clon de cada repo (el índice del worktree vive
  ahí) y el temporal y las cachés de la tarea (`runs/<id>/tmp`, `runs/<id>/cache`: `GOPATH`, `GOMODCACHE`, `GOCACHE`,
  `npm_config_cache`, `XDG_CACHE_HOME` y los archivos de semgrep). Nunca las cachés ni el temporal del usuario. El
  perfil de herramientas escribe solo el temporal y las cachés.
- **Escritura denegada:** de cada clon, `.git/hooks`, `.git/config`, `.git/commondir`, `.git/packed-refs`,
  `.git/info`, `.git/objects/info`, `.git/refs/replace` y `.git/shallow` (`rutasGitProtegidas`), y el gitdir y la ref
  de la rama de cada otra tarea que comparte el clon (`rastrosDeOtrasTareas`, recalculado en cada perfil); el archivo `.git` de cada
  worktree y `commondir`, `gitdir` y `config.worktree` de su gitdir, por ruta literal; `.claude` y `CLAUDE.local.md` de
  cada worktree; el binario de la fábrica, `fabrica.yaml`, `runs/*/*.json` (estado, base, settings y MCP de la tarea),
  `runs/.cache`, el cwd de los agentes, `/tmp/claude` y `~/.npm` (srt los deja escribibles sin listarlos). Una ruta
  cuya carpeta es un enlace (en macOS `/var` es `/private/var`) va también resuelta: sin eso, el deny de un archivo
  todavía inexistente no alcanzaba (probado).
- **Lectura denegada:** las credenciales de `credenciales()` (gcloud, gh, ssh, aws, docker, kube, `.git-credentials`,
  `.netrc`, `.npmrc`, la configuración de la fábrica y `~/Library/Keychains`), más `~/Documents`, `~/.claude`,
  `~/Library/Application Support` y `~/Library/Cookies`, con `allowRead` de la carpeta de la tarea, de los clones del
  cliente y de la telemetría de Go. Probado: sin el deny del llavero, `git credential fill` dentro de `srt` devolvía la
  contraseña de GitHub del usuario; con él, no.
- **Red:** ver la tabla. Nunca `github.com`. Los sockets Unix no se habilitan: el socket de Docker queda fuera.
- **Apple Events:** `allowAppleEvents: false`, explícito. Probado: `osascript` hacia System Events y `open -a` fallan
  dentro de `srt` (error -10810).
- **Entorno:** Go con `GOTOOLCHAIN=local`, `GOENV=off`, `GOPROXY=https://proxy.golang.org` y `GOVCS=*:off`. `TMPDIR` es
  el temporal de la tarea dentro del encierro; `srt` mismo usa el de afuera para su socket (con la ruta de la tarea, el
  socket puede pasar el largo máximo; probado).

Antes de gastar al Builder, la tarea escribe los dos perfiles y los sondea: `srt` tiene que arrancar, `curl` a
`https://github.com` tiene que fallar y una escritura en `$HOME` también. Si algo de eso no se cumple, la tarea se
detiene como falla de infraestructura. Ningún camino cae a ejecutar sin encierro: `RunCheck`, `arrancarApp` y el
escaneo sin perfil o sin `srt` no ejecutan nada. Al terminar cada chequeo, cada herramienta y cada agente, la fábrica
mata el grupo de procesos completo, aunque el contexto no se haya cancelado.

## Git de la fábrica

La fábrica corre git afuera del encierro sobre worktrees y clones que el código de la rama puede escribir. Cada git (y
el que lanza gitleaks) fija `core.fsmonitor`, `core.hooksPath`, `core.pager`, `core.sshCommand` y
`core.attributesFile=/dev/null` por `GIT_CONFIG_COUNT`, sin la config global ni la del sistema (`GIT_CONFIG_GLOBAL=/dev/null`,
`GIT_CONFIG_NOSYSTEM`, `GIT_ATTR_NOSYSTEM`): así un `diff.external`, un filtro o un `textconv` de `~/.gitconfig` no se
ejecutan aunque alguien cambie ese archivo. La identidad del rebase (`aldia.go`) se lee una vez de la config global del
usuario y se fija por `-c`. Además, `GIT_NO_REPLACE_OBJECTS=1` y `GIT_GRAFT_FILE=/dev/null`: la fábrica ve los objetos
y la historia reales. El primero no cubre `info/grafts` (probado con git 2.54); el segundo sí. Los agentes que leen
reciben el mismo entorno más `GIT_ATTR_SOURCE` en el árbol vacío: los `.gitattributes` de la rama no cuentan (probado:
con `* -diff` commiteado, `git diff` muestra "Binary files differ"; con `GIT_ATTR_SOURCE`, el texto). El Builder
recibe solo `GIT_NO_REPLACE_OBJECTS` y `GIT_GRAFT_FILE` (conserva su config global por la identidad de sus commits). Sobre un worktree usa `GIT_DIR`, `GIT_COMMON_DIR` y `GIT_WORK_TREE` explícitos, después de
verificar que su `.git` apunte a `<clon>/.git/worktrees/<n>` de un clon de la fábrica, que `commondir` y `gitdir` sean
los de `git worktree add` y que el clon no active `config.worktree`. Sobre un clon fija su propio `.git` y, si alguien
le plantó un `.git/commondir`, no corre. Si algo no coincide, la tarea se traba por seguridad.

## Lo que se revisa es lo que se sube

Revisión del 23/11: el Builder podía commitear X y correr `git replace X Y` con Y benigno; todo diff, escaneo y
revisión veía Y, y el push mandaba X. Además del entorno de arriba:

- `.git/refs/replace`, `packed-refs`, `info`, `objects/info` y `shallow` de los clones no se escriben desde `srt` ni
  desde el sandbox del Builder, ni con Edit. Probado con `srt` 0.0.83: adentro `git commit`, `git rebase` y `git commit
  --amend` andan, y un commit con `gc.auto=1` termina en 0 (no se comprobó qué hizo el gc); `git replace`, `git pack-refs`, escribir `info/attributes` u
  `objects/info/alternates` fallan. Un `git gc` o `git repack` explícito también falla (escribe `packed-refs` y
  `objects/info`).
- Guardar lo aprobado y la aprobación fallan si el clon tiene refs bajo `refs/replace/` o alguno de
  `objects/info/alternates`, `objects/info/http-alternates`, `info/grafts`, `info/attributes` o `shallow` con contenido
  (`integridadGit`).
- El commit de cada repo se fija apenas pasan el chequeo y la verificación de cambios sin commitear, antes del escaneo, la
  app de UX y los revisores (`fijarRevisados`). El escaneo (gitleaks, semgrep, los diffs) y el prompt de los revisores
  usan ese sha, no `HEAD`. Al guardar, HEAD tiene que seguir en ese sha, la rama también y el worktree sin cambios; si
  no, la tarea se traba por seguridad y no se aprueba. La app de UX se baja antes del revisor del resultado, y éste
  vuelve los worktrees al sha fijado.
- Los revisores tienen la indicación de usar `git diff --text`.

## Tareas que comparten clon

Las tareas de un mismo repo comparten el clon (`<espacio>/clones/<repo>`). Se eligió no pasar a un clon por tarea: la
ruta del clon está en unos ocho lugares y en la verificación de los worktrees. En cambio, cada perfil de `srt` y cada
sandbox del Builder niegan escribir el gitdir (`.git/worktrees/<n>`) y la ref (`refs/heads/factory/<id>`) de cada otra
tarea que exista al escribirlo (cada vuelta y cada vista previa); `packed-refs` ya está negado entero. Edit solo
escribe en los worktrees de la propia tarea, así que `runs/<otra>/**` queda fuera. Lo que quede abierto es negación de
servicio: la aprobación sube el sha guardado, verifica que la rama siga ahí y falla si no.

## La base de la revisión

Al crear los worktrees, la fábrica guarda el sha de `origin/main` de cada repo en `runs/<id>/base.json`. El escaneo,
gitleaks (`<sha>..HEAD`), semgrep (`--baseline-commit`), el diff que reciben los revisores y lo que muestra la
aprobación usan ese sha, no el ref: el ref vive en el clon, que el Builder escribe. La base solo avanza a un `main` que
la fábrica acaba de traer y que la rama ya contiene. Una tarea de antes de `base.json` toma el merge-base entre
`origin/main` y la rama al reanudarse.

## Herramientas del escaneo

- **gitleaks** usa la config de la fábrica (las reglas por defecto), `--ignore-gitleaks-allow` y una carpeta vacía como
  `.gitleaksignore`. gitleaks 8.30.1 lee además el `.gitleaksignore` de la carpeta que escanea aunque se le indique otro
  (probado): por eso escanea una carpeta vacía de la fábrica y encuentra el repo por `GIT_DIR`. Probado: con
  `.gitleaks.toml`, `.gitleaksignore` y `gitleaks:allow` en la rama, el secreto se reporta igual.
- **semgrep** usa las reglas del registro que elige la fábrica con `--disable-nosem`, `--x-ignore-semgrepignore-files`
  y `--no-git-ignore` (probado: un hallazgo marcado `nosemgrep` y listado en `.semgrepignore` aparece). La segunda es una
  opción interna de semgrep 1.183; si desaparece, semgrep falla y queda "sin revisar". El semgrep del chequeo
  (`.semgrep` del repo) replica al CI y respeta lo que el CI respete.
- Una herramienta que no está o no corre queda "sin revisar", lo que impide el merge automático.

## Revisor de UX y Playwright

El revisor de UX usa una lista cerrada de herramientas (`playwrightPermitidas` en `internal/fab/uxui.go`): navegar,
capturas, clics, escritura, teclas, consola y red. Las que ejecutan código (`browser_evaluate`,
`browser_run_code_unsafe`), suben o guardan archivos o cambian el estado del navegador están además en `deny`. Un hook
(`hooks/playwright-sin-archivos.sh`) niega cualquier llamada con `filename` o `paths`: el servidor corre fuera de todo
sandbox y escribiría donde le diga el agente. Lo que guarda sin nombre va a `--output-dir`, dentro de la tarea. El MCP
se genera por vuelta (`runs/<id>/mcp-playwright-v<n>.json`) con el origen de la app de esa vuelta.

## Docker

Docker no está disponible dentro del encierro. Los contenedores de prueba los levanta la fábrica, fuera, a partir de
`servicios` en `fabrica.yaml`; nunca desde archivos de la rama. Un chequeo que dependa de lanzar contenedores propios
falla dentro del encierro: esos servicios se declaran en `fabrica.yaml`.

## Aprobación y panel

Publicar en GitHub requiere `fabrica aprobar` (terminal o botón del panel) y una confirmación por un canal que los
agentes no alcanzan: el diálogo del sistema o la terminal de control. El diálogo muestra un encabezado fijo con los
repos, el título de la tarea en una línea saneada y, en macOS, pide escribir el id de la tarea. Desde `srt` no se pueden
mandar Apple Events (probado), así que el código encerrado no puede manejar ese diálogo con `osascript`; en el sandbox
de Claude Code están bloqueados por defecto según su documentación (no probado). El panel acepta un pedido de
aprobación por tarea cada cinco minutos y, con su propia cuenta, un relevantado de la vista previa cada cinco minutos.
`/api/abrir` abre solo la URL de una PR guardada en el `prs.json` de alguna tarea o una página de la vista previa
registrada en `preview.json`.

## Riesgos residuales

- **Mismo usuario.** Todo lo que escape de un encierro, o lo que corre sin encierro (los procesos `claude`, el MCP de
  Playwright, `ui-scan`, el despliegue), tiene los permisos del usuario. Los agentes sin Bash libre están contenidos por
  los permisos de Claude Code, no por el sistema operativo.
- **localhost abierto** para el código encerrado y el Builder (en macOS es todo o nada): alcanza el panel, los servicios
  de prueba y cualquier otro servicio local de la máquina.
- **Los registros como salida.** El código encerrado puede mandar datos en una URL a `registry.npmjs.org` o
  `proxy.golang.org`. Lo que puede leer es acotado (sin credenciales ni `~/Documents` fuera de la tarea), pero incluye el
  código del cliente de la tarea.
- **Perfil de herramientas.** Tiene `semgrep.dev`, `vuln.go.dev`, `storage.googleapis.com` (cualquiera puede tener un
  bucket público ahí) y trustd (una posible salida, según la documentación de `srt`). No ejecuta código de la rama; sí
  procesa archivos de la rama. Es una decisión de diseño: la alternativa era dejar esas herramientas "sin revisar".
- **Lectura amplia de los agentes.** El sandbox del Builder deja leer todo el espacio (otros clientes incluidos) y la
  fábrica; la herramienta Read de los agentes también. Las listas de `~/Documents` y `~/.claude` de la herramienta Read
  se calculan al escribir los settings: lo que aparezca después queda leíble hasta la siguiente vez.
- **Procesos que se escapan del grupo.** Un proceso que crea su propia sesión (`setsid`) sobrevive al corte del grupo.
  Sigue dentro de `srt`; lo que puede hacerle a lo aprobado lo frenan el sha fijado y `integridadGit`.
- **Tareas del mismo repo.** Una tarea (o la vista previa de una tarea, o un proceso que quedó) puede mover la rama o el
  gitdir de otra que nació después de escribir su perfil: la otra no se aprueba (negación de servicio), no se sube
  nada distinto.
- **Config global de git.** La fábrica ya no la lee (salvo la identidad): un `filter.lfs.*` o un `core.excludesFile`
  del usuario no se aplican en sus checkouts, rebases y `status`; un repo con LFS saldría con los punteros y un
  ignore global no contaría para "cambios sin commitear" (el Builder sí lo ve). En esta máquina no hay ninguno de los dos.
- **Escaneo y archivos del worktree.** gosec, semgrep y govulncheck leen archivos del worktree, no del commit fijado.
  Corren antes de levantar la app de UX, que es el primer código de la rama que corre después de fijar el commit.
- **`.gitattributes` de la rama** sigue valiendo para el git de la fábrica y del Builder (checkout, rebase). Los
  lectores lo ignoran por `GIT_ATTR_SOURCE`; gitleaks encontró igual un secreto con `* -diff` commiteado (probado).
- **`fabrica preguntar`** del Spec writer corre fuera del sandbox (`excludedCommands`). Un `$(...)` en su argumento
  también, si Claude Code lo aprueba como comando de lectura: es lo mismo que podía hacer antes, sin sandbox.
- **`--allowed-origins`** no es una frontera de seguridad según Playwright; el hook depende de los nombres de los
  parámetros de la versión fijada (0.0.83).
- **`mktemp` de macOS** sin plantilla ignora `TMPDIR` y usa el temporal del usuario, que ya no es escribible: dentro del
  encierro falla. Se usa `mktemp "$TMPDIR/x.XXXXXX"`.
- **Go en el sandbox del Builder** no descarga módulos (sin trustd): la fábrica los baja antes de cada vuelta y antes
  de cada chequeo; una dependencia nueva que agregue el Builder no está disponible en su Bash ni en el hook Stop (que
  no baja módulos) hasta el chequeo de la fábrica. Un error `tls: failed to verify certificate` cuenta como del
  entorno, no como un test rojo.
- **Cachés de otras herramientas.** Lo que use `os.UserCacheDir` en macOS (`~/Library/Caches`, por ejemplo
  golangci-lint sin `GOLANGCI_LINT_CACHE`) no puede escribir su caché dentro del encierro.
- **Despliegue con las credenciales del usuario**, sobre código ya integrado en `main`.
- **No probado:** el sandbox de Claude Code con estos settings (`TMPDIR`, `allowRead`, Apple Events) y la carga de
  configuración desde `--add-dir` con `claude` real; Linux (bubblewrap), donde `srt` no aplica los comodines de
  escritura. Del 23/11, verificado solo contra la documentación de Claude Code 2.1.330, sin correr `claude -p`: que el
  sandbox aplique en `-p` (la documentación no lo dice explícitamente), que en `dontAsk` se niegue el Edit fuera de los
  `allow` de `Edit(//<worktree>/**)`, que un `denyWrite` gane a la escritura por defecto de un `--add-dir` (probado solo
  con `srt`: un `denyWrite` gana a un `allowWrite` más específico), que `allowedDomains: []` cierre la red, la sintaxis
  de `excludedCommands`, que los deny de Bash de los lectores (`Bash(git * --output*)` y demás) frenen esas formas, y
  que `autoMemoryEnabled: false` apague la memoria automática (la documentación dice que `-p` sin `--bare` la carga).
  Prueba manual sugerida: una tarea de prueba y mirar en `runs/<id>/04-*.rastro.log` que `git grep -O` y
  `sort -o` aparezcan negados y que un `touch <worktree>/x` desde el Bash de un revisor falle.
