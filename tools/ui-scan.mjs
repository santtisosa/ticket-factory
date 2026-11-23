// ui-scan: lo determinista antes que el LLM. Por cada ruta saca capturas de escritorio y celular,
// el árbol de accesibilidad y las violaciones de axe (WCAG 2.2 AA).
// Uso: node ui-scan.mjs <baseURL> <outDir> <ruta> [<ruta>...]
// Inicio de sesión opcional, desde el entorno: con FABRICA_APP_USUARIO y FABRICA_APP_CLAVE definidos,
// hace POST de {email, password} en JSON a FABRICA_APP_LOGIN (por defecto /api/auth/login) y sigue
// con esa sesión. Sin usuario, escanea sin iniciar sesión.
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright";
import { AxeBuilder } from "@axe-core/playwright";

const [baseURL, outDir, ...rutas] = process.argv.slice(2);
if (!baseURL || !outDir || rutas.length === 0) {
  console.error("uso: node ui-scan.mjs <baseURL> <outDir> <ruta>...");
  process.exit(2);
}
const usuario = process.env.FABRICA_APP_USUARIO || "";
const clave = process.env.FABRICA_APP_CLAVE || "";
const rutaLogin = process.env.FABRICA_APP_LOGIN || "/api/auth/login";
mkdirSync(outDir, { recursive: true });

const VISTAS = { escritorio: { width: 1440, height: 900 }, celular: { width: 390, height: 844 } };
const slug = (r) => (r.replace(/[^a-z0-9]+/gi, "-").replace(/^-|-$/g, "") || "inicio");

const browser = await chromium.launch(); // sin ventana
const resumen = [];
try {
  for (const [vista, viewport] of Object.entries(VISTAS)) {
    const ctx = await browser.newContext({ baseURL, viewport, reducedMotion: "reduce" });
    const page = await ctx.newPage();
    if (usuario) {
      const login = await page.request.post(rutaLogin, { data: { email: usuario, password: clave } });
      if (!login.ok()) throw new Error(`inicio de sesión de ${usuario} en ${rutaLogin}: ${login.status()}`);
    }
    for (const ruta of rutas) {
      const base = join(outDir, `${slug(ruta)}-${vista}`);
      const res = await page.goto(ruta, { waitUntil: "networkidle" });
      await page.mouse.move(-1, -1);
      await page.screenshot({ path: `${base}.png`, fullPage: true });
      const item = { ruta, vista, status: res?.status() ?? 0, captura: `${base}.png` };
      if (vista === "escritorio") {
        writeFileSync(`${base}.aria.yml`, await page.locator("body").ariaSnapshot());
        const axe = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze();
        const violaciones = axe.violations.map((v) => ({
          regla: v.id, impacto: v.impact, ayuda: v.help, nodos: v.nodes.slice(0, 5).map((n) => n.target.join(" ")),
        }));
        writeFileSync(`${base}.axe.json`, JSON.stringify(violaciones, null, 2));
        item.axe = violaciones.length;
        item.aria = `${base}.aria.yml`;
      }
      resumen.push(item);
    }
    await ctx.close();
  }
} finally {
  await browser.close();
}
writeFileSync(join(outDir, "resumen.json"), JSON.stringify(resumen, null, 2));
console.log(JSON.stringify(resumen));
