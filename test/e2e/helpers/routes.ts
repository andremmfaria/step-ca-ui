import * as fs from "node:fs";
import * as path from "node:path";
import { REPO_ROOT } from "./env";

export interface PostRoute {
  /** The chi pattern as registered, e.g. "/renew/{id}". */
  pattern: string;
  handler: string;
}

/**
 * E2E-CSRF-01 derives its route list from the router rather than hardcoding it,
 * so a new POST route with no CSRF gate fails the sweep on the day it is added.
 * The repo is bind-mounted into the harness, so main.go is readable at test time.
 */
export function postRoutes(): PostRoute[] {
  const mainGo = fs.readFileSync(path.join(REPO_ROOT, "backend", "main.go"), "utf8");
  const routes: PostRoute[] = [];
  const re = /r\.Post\(\s*"([^"]+)"\s*,\s*([A-Za-z0-9_.]+)\s*\)/g;
  for (let m = re.exec(mainGo); m !== null; m = re.exec(mainGo)) {
    if (m[1] && m[2]) routes.push({ pattern: m[1], handler: m[2] });
  }
  if (routes.length === 0) {
    throw new Error("derived zero POST routes from main.go; the sweep would pass vacuously");
  }
  return routes;
}

/** Substitutes a concrete id for a chi {id} placeholder. */
export function withID(pattern: string, id: string | number): string {
  return pattern.replace(/\{[^}]+\}/g, String(id));
}
