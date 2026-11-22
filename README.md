```
   __  _      __        __        ____           __
  / /_(_)____/ /_____  / /_      / __/___ ______/ /_____  _______  __
 / __/ / ___/ //_/ _ \/ __/_____/ /_/ __ `/ ___/ __/ __ \/ ___/ / / /
/ /_/ / /__/ ,< /  __/ /_/_____/ __/ /_/ / /__/ /_/ /_/ / /  / /_/ /
\__/_/\___/_/|_|\___/\__/     /_/  \__,_/\___/\__/\____/_/   \__, /
                                                            /____/
```

**Una línea de montaje de agentes que lleva un ticket hasta una rama lista para tu "dale".**

Le das un ticket: un issue de GitHub, una tarjeta de un tablero o una frase en la terminal. La fábrica escribe la spec,
implementa en una rama aislada y corre los tests de tu repo. Después la hace revisar por varios agentes y decide con
reglas fijas si está lista.

Nada sale de tu máquina hasta que vos digas `dale`. Recién ahí pushea y abre la PR.

Cada puesto de la línea es un proceso de [Claude Code](https://code.claude.com) en modo headless (`claude -p`), con su
propio prompt, sus herramientas y su esquema de salida. Los encadena un orquestador en Go que **no es un modelo**: no
razona, no improvisa y no aprueba nada por su cuenta.

> **Estado: en construcción.** La fábrica existe y se usa a diario sobre repos reales; ahora se está separando
> de ese uso para que sirva a cualquiera. Este repo tiene por ahora la documentación de cómo va a funcionar; el
> código se publica cuando pase sus pruebas en un Linux limpio. Lo que describe este README es el objetivo de la
> primera versión.

---

## Qué hace

```
 issue / tablero / prompt
          │
          ▼
     ┌─────────┐   ┌─────────────┐   ┌─────────┐   ┌─────────┐   ┌───────────┐   ┌──────────┐
     │ entrada │──▶│ spec writer │──▶│ builder │──▶│ chequeo │──▶│ revisores │──▶│ approver │
     └─────────┘   └─────────────┘   └─────────┘   └─────────┘   └───────────┘   └──────────┘
                                          ▲        tus tests    seguridad,       reglas en Go
                                          │                     código, ux, ui        │
                                          └────────── vuelve con el feedback ─────────┤
                                                         (máx. 2 vueltas)             ▼
                                                                               ESPERA_TU_DALE
                                                                                      │ fabrica dale
                                                                                      ▼
                                                                           push + PR + CI → merge
```

1. **Entrada.** Junta lo que llega, saca duplicados y tapa datos personales (mails, teléfonos, tokens) antes de que lo
   lea un modelo. Lo que viene de un log o de un tercero entra como `PROPUESTA` y no arranca hasta que lo aceptes.
2. **Spec writer.** Escribe una spec de una página con criterios de aceptación `CA-1`, `CA-2`…
   - Si una duda cambia el alcance, frena y te pregunta en vez de inventar.
   - Le contestás con `fabrica continuar <id> "<respuestas>"`.
3. **Builder.** Implementa en un worktree propio, dentro del sandbox de Claude Code.
   - Escribe solo en la rama de la tarea, sin push y con la red restringida.
   - Escribe un test por criterio.
4. **Chequeo.** La fábrica corre el comando de tests de tu repo **afuera** del sandbox, con Docker si hace falta. Si
   queda rojo, el Builder retoma su sesión con la salida y lo arregla sin gastar una vuelta.
5. **Revisores.**
   - Seguridad y código revisan siempre. UX y UI, si la tarea toca pantallas y tu repo sabe levantarse.
   - Cada hallazgo necesita evidencia (`archivo:línea`): sin evidencia no hay hallazgo.
   - Si los tenés instalados, también corren gitleaks, semgrep, gosec, govulncheck y `npm audit`.
6. **Approver.** Es código Go, no un modelo.
   - Pasa si el Builder terminó, el chequeo está verde y no hay hallazgos graves con evidencia.
   - Si no, le devuelve al Builder la lista concreta, hasta dos vueltas.
7. **Tu dale.** Mirás la rama y decidís; si configuraste la vista previa, también podés ver la app andando.
   `fabrica dale` pushea exactamente el commit aprobado, abre la PR y dispara el CI.
8. **Después del merge** (todo opcional):
   - Despliega con el comando que configuraste y prueba que la app responda.
   - Redacta un aviso para quien pidió el cambio: lo ves con `fabrica aviso <id>` y lo mandás con `--mandar`, por
     mail o como comentario en el issue.
9. **Cerebro** (opcional). El Bibliotecario anota en una carpeta de markdown tuya lo que aprendió de cada tarea, y
   el Jefe contesta preguntas sobre esa carpeta citando el archivo y la línea (`fabrica ask "<pregunta>"`). El Spec
   writer y el Builder la leen antes de trabajar.

## Por qué así

- **Un humano antes de que algo salga.** Los agentes leen texto de terceros (tickets, logs), así que ninguno tiene
  credenciales de push ni de mail. Lo que sale de la máquina lo hace el código Go, después de tu dale.
- **Reglas fijas para aprobar.** Un modelo puede opinar, pero no aprobar. El approver es una función determinista que
  podés leer (`internal/fab/approver.go`) y volver a correr sobre tareas viejas (`fabrica replay`).
- **Fácil de auditar.** Todo lo de una tarea queda en `runs/<id>/`: la entrada, la spec, cada salida, el costo y un
  `events.jsonl`. La página en `http://127.0.0.1:7171` muestra el diagrama y marca el puesto que está trabajando.
- **Topes de gasto.** Por llamada y por día.

## Requisitos

| | Para qué | ¿Obligatorio? |
|---|---|---|
| macOS o Linux | El sandbox de Claude Code usa Seatbelt en macOS y bubblewrap en Linux. Windows nativo no. | sí |
| [Claude Code](https://code.claude.com) | Cada puesto es un `claude -p`. Lo instalás vos y entrás con **tu** cuenta. `fabrica doctor` te dice si tu versión alcanza. | sí |
| Go 1.26 o más | Para compilar. | sí |
| git y [gh](https://cli.github.com), con `gh auth login` | Worktrees, PRs y CI. | sí |
| Docker | Para los servicios de test de tu proyecto (una base, por ejemplo), la vista previa y UX/UI. | no |
| Node | Para los revisores de UX y UI (Playwright). | no |
| gitleaks, semgrep, gosec, govulncheck | Escaneo extra. Si falta uno, la revisión lo dice ("sin revisar") y no aprueba en silencio. | no |

## Instalación

```bash
git clone https://github.com/santtisosa/ticket-factory
cd ticket-factory
make build          # compila ./fabrica con la página embebida
./fabrica doctor    # revisa claude, la autenticación, gh, git, Docker y el sandbox
```

## Primer ticket

```bash
mkdir ~/mi-fabrica && cd ~/mi-fabrica
fabrica init                                # crea un fabrica.yaml de ejemplo
$EDITOR fabrica.yaml                        # apuntalo a tu repo
fabrica add --proyecto demo "El endpoint /health devuelve la versión de la app"
fabrica run --once                          # procesa esa tarea y termina
fabrica ver <id>                            # qué hizo, cuánto costó y en qué rama quedó
fabrica dale <id> --seco                    # muestra qué pushearía, sin pushear
```

Cuando te convenza: `fabrica dale <id>`.

## Configuración: `fabrica.yaml`

Un archivo por espacio de trabajo. **Nunca pongas secretos acá**: van por variables de entorno o en
`~/.config/ticket-factory/env`, con permisos 600.

```yaml
proyectos:
  demo:
    repos:
      - nombre: demo-api
        ruta: git@github.com:tu-usuario/demo-api.git
        check: make check            # lo que tiene que dar verde; si falta, la fábrica lo deduce
        deploy: ./scripts/deploy.sh  # opcional: corre después del merge
        smoke: https://api.demo.example.com/health
      - nombre: demo-web
        ruta: git@github.com:tu-usuario/demo-web.git
        check: npm run lint && npm test
        frontend: true               # activa los revisores de UX y UI
    servicios:                       # opcional: lo que tus tests necesitan levantado (con Docker)
      - imagen: mysql:8.4
        puerto: 3306
        exporta: { TESTDB_DSN: "root:root@tcp(127.0.0.1:{puerto})/" }
    preview:                         # opcional: levantar la app de la rama para mirarla antes del dale
      comandos: ["npm run dev -- --port {puerto}"]
    aviso:
      para: quien-pidio@example.com  # o el autor del issue, si el canal es "comentario"

fuentes:
  github-issues:
    repo: tu-usuario/demo-api
    etiqueta: fabrica                # solo los issues con esta etiqueta
    autores: [tu-usuario]            # y solo de estas personas
  betterstack: {}                    # incidentes de producción, como PROPUESTA (token por env)
  # exec: un comando tuyo que imprime tickets en JSON, para cualquier otro tablero (ver docs/fuentes.md)

aviso:
  canal: smtp                        # smtp | comentario | exec; el servidor va por env (FABRICA_SMTP_*)
  de: fabrica@example.com

cerebro:
  repo: ~/notas-del-equipo           # un repo git con markdown; opcional
  escribibles: ["lecciones.md", "modulos/"]

reglas: reglas-de-la-casa.md         # se suman a los prompts: tu stack, tus convenciones, qué no tocar
diseno: DISENO.md                    # opcional: lo que miran los revisores de UI

merge:
  solo: false                        # true: mergea sola lo no sensible con el check requerido en verde
  check_requerido: ci-ok
  sensibles: ["**/migrations/**", "**/auth/**", ".github/**"]

topes:
  por_llamada_usd: 3
  por_dia_usd: 20
```

## Comandos

| Comando | Qué hace |
|---|---|
| `fabrica add [--proyecto <p>] "<tarea>"` | Carga una tarea |
| `fabrica status` | Muestra una fila por tarea |
| `fabrica ver <id>` | Muestra de dónde vino la tarea, sus eventos, el costo y los archivos |
| `fabrica run [--once]` | Procesa la cola, una tarea por proyecto a la vez |
| `fabrica serve [--abrir]` | Junta `run`, la lectura de fuentes y la página en un solo proceso |
| `fabrica web [--abrir]` | Levanta solo la página |
| `fabrica ingestar` | Lee las fuentes una vez (solo lectura) |
| `fabrica aceptar <id>` / `descartar <id>` | Decide sobre una `PROPUESTA` |
| `fabrica continuar <id> "<respuestas>"` | Retoma una tarea cuya spec frenó con preguntas |
| `fabrica reintentar <id>` / `cancelar <id>` | Devuelve una `TRABADA` a la cola, o la descarta |
| `fabrica prioridad <id> <n>` | Cambia la prioridad: más alta, antes |
| `fabrica pausar` / `seguir` | Deja de tomar tareas nuevas, o vuelve a tomarlas |
| `fabrica preview <id>` / `preview bajar` | Levanta la app de una tarea que espera tu dale, o la baja |
| `fabrica dale <id> [--seco]` | **Tu dale**: pushea lo aprobado, abre la PR y corre el CI |
| `fabrica prs` | Muestra el estado de CI y de merge de las PRs abiertas |
| `fabrica mergeado <id>` | Cierra una tarea que mergeaste a mano en un repo sin GitHub |
| `fabrica desplegar <id> [--seco]` | Despliega una tarea mergeada con el comando del repo |
| `fabrica aviso <id> [--mandar] [--para <mail>]` | Muestra el aviso de una tarea en producción; `--mandar` lo envía |
| `fabrica ask "<pregunta>"` | Le pregunta al Jefe; contesta con citas verificadas del cerebro |
| `fabrica aprender <ruta>` | El Bibliotecario destila una carpeta de referencia en el cerebro |
| `fabrica bibliotecario <id>` / `lint-cerebro` / `indice` | Aprende de una tarea cerrada / busca contradicciones / rehace el índice |
| `fabrica cerebro-sync [--seco]` | Lleva lo aprendido al repo del cerebro como rama y PR en borrador |
| `fabrica probar-app` | Levanta la app desde main, la escanea y la baja (sin agentes) |
| `fabrica replay` / `metricas` | Re-decide vueltas viejas con las reglas de hoy, o muestra costos y frenos |
| `fabrica limpiar` | Saca los worktrees de las tareas cerradas |
| `fabrica init` / `doctor` | Crea `fabrica.yaml`, o revisa que esté todo |

## Estados de una tarea

```
PROPUESTA ─aceptar─▶ PENDIENTE ─▶ EN_CURSO ─▶ ESPERA_TU_DALE ─dale─▶ PR_ABIERTA ─▶ CI_VERDE ─▶ MERGEADA
                                     │                                                             │
                                     └─▶ TRABADA  (con el motivo: reintentar o continuar)         ▼
                                                                                     DEPLOY_PENDIENTE (si hay deploy)
```

## Qué sale de tu máquina

| Qué | Hacia dónde | Cuándo |
|---|---|---|
| Prompts y código del repo | Anthropic | En cada puesto (es Claude Code) |
| Paquetes | npm y el proxy de Go | El Builder, solo hacia una lista cerrada de dominios. El chequeo (`npm ci`, `go test`) corre fuera del sandbox, con red abierta |
| Reglas y bases de vulnerabilidades | semgrep (`p/golang`, `p/typescript`, `p/owasp-top-ten`), npm audit, govulncheck | En la revisión, si los tenés instalados |
| Playwright MCP | npm (`npx @playwright/mcp`) | El revisor de UX, si hay `frontend: true` |
| Lectura de issues, proyectos y PRs | GitHub, con tu `gh` | Al leer las fuentes y al seguir las PRs |
| El push de la rama y la PR | GitHub | **Solo** con `fabrica dale` |
| El merge | GitHub | Solo si activaste `merge.solo` y el check requerido está en verde |
| El deploy | Lo que haga tu comando | Solo si configuraste `deploy:` |
| El aviso | Tu servidor SMTP o GitHub | Solo con `fabrica aviso <id> --mandar` |
| El cerebro | El remoto de tu repo de notas | Solo con `fabrica cerebro-sync` |

## Costos

El uso de Claude Code lo pagás vos, con tu cuenta.
- Como referencia, en el uso real una tarea chica cuesta entre US$ 3 y US$ 15 y tarda de 20 a 30 minutos. Una que
  necesita dos vueltas cuesta y tarda más o menos el doble.
- `fabrica metricas` muestra el costo por puesto y por tarea.
- Los topes de `fabrica.yaml` cortan antes de pasarse.

## Seguridad

- **Tratá los tickets como texto no confiable.** Cualquiera puede escribir instrucciones dentro de un issue. La fábrica
  se los pasa a los agentes marcados como datos, nunca como órdenes. Usá `autores:` para limitar quién puede cargar
  trabajo.
- **El Builder corre en el sandbox de Claude Code**, con `failIfUnavailable`: si el sandbox no arranca, la tarea no corre.
- **El chequeo corre fuera del sandbox**, con Docker y red, y ejecuta el código de tu repo más lo que escribió el
  Builder. Usá la fábrica solo sobre repos en los que confiás, mejor en una máquina o VM dedicada.
- **Los agentes no ven tus credenciales.** No les llega ninguna variable de entorno con secretos, y no pueden leer
  `~/.ssh`, `~/.aws`, `~/.config/gh` ni tu archivo de credenciales.

## Claude Code y tu cuenta

ticket-factory corre Claude Code, pero no lo incluye ni lo modifica: lo instalás aparte y entrás con tu propia cuenta.
- Para uso automatizado, la documentación de Anthropic recomienda una API key de la
  [Claude Console](https://platform.claude.com), o Bedrock, Vertex o Foundry.
- Si usás una suscripción, revisá antes los [términos de uso](https://code.claude.com/docs/en/legal-and-compliance).
- Sos responsable de lo que hagan los agentes que corrés.

Este proyecto no está hecho, avalado ni asociado con Anthropic.

## Limitaciones

- Está pensada para GitHub (usa `gh`). GitLab y otros, todavía no.
- Corre una tarea por proyecto a la vez.
- Los revisores de UX y UI necesitan que tu repo se pueda levantar con un comando.
- Todo está en español: comandos, estados, prompts y la página.

## Licencia

[MIT](LICENSE).
