/* Real backend API client. Maps server responses to the shapes app.js consumes. */
const API = (() => {
  async function j(method, url, body) {
    const opt = { method, headers: {} };
    if (body !== undefined) { opt.headers["Content-Type"] = "application/json"; opt.body = JSON.stringify(body); }
    const r = await fetch(url, opt);
    if (!r.ok) { const e = new Error(`${method} ${url} → ${r.status}`); e.status = r.status; throw e; }
    const ct = r.headers.get("content-type") || "";
    return ct.includes("json") ? r.json() : r.text();
  }

  async function getMachines() { return j("GET", "/api/machines"); }

  async function getInstalls() {
    const rows = await j("GET", "/api/installs");
    return rows.map(x => ({
      id: x.id, software: x.software, machine: x.machine, kind: x.kind || "custom",
      current_version: x.current_version || "—", latest_version: x.latest_version,
      status: x.status, behind_count: x.behind_count || 0,
      update_kind: x.update_kind, error: x.error,
    }));
  }

  async function getChangelog(software, version) {
    return j("GET", `/api/changelog/${encodeURIComponent(software)}/${encodeURIComponent(version)}`);
  }

  function jobFromRow(x) {
    return {
      id: String(x.id), software: x.software, machine: x.machine,
      kind: x.kind, runner: x.runner,
      prompt: x.cmd || null,                          // server stores the rendered command
      status: x.status, new_version: x.new_version,
      installId: `${x.software}::${x.machine}`,
      started_at: x.started_at,
      log: x.log ? x.log.replace(/\n$/, "").split("\n") : [],
    };
  }
  async function getJobs(limit = 30) {
    const rows = await j("GET", `/api/jobs?limit=${limit}`);
    return rows.map(jobFromRow);
  }
  async function getJob(id) { return jobFromRow(await j("GET", `/api/jobs/${id}`)); }

  async function createJob(software, machine) {
    const r = await fetch(`/api/installs/${encodeURIComponent(software)}/${encodeURIComponent(machine)}/update`, { method: "POST" });
    if (r.status === 409) { const e = new Error("conflict"); e.status = 409; throw e; }
    if (!r.ok) { const e = new Error(`update → ${r.status}`); e.status = r.status; throw e; }
    return (await r.json()).job_id;
  }
  async function abortJob(id) { return jobFromRow(await j("POST", `/api/jobs/${id}/abort`)); }
  async function check() { return j("POST", "/api/check"); }

  /* SSE: onLog(line), onDone(statusString). Returns the EventSource (close to stop). */
  function streamJob(id, onLog, onDone) {
    const es = new EventSource(`/api/jobs/${id}/log/stream`);
    es.addEventListener("log", e => onLog(e.data));
    es.addEventListener("done", e => { onDone(e.data); es.close(); });
    es.addEventListener("error", () => { es.close(); });
    return es;
  }

  return { getMachines, getInstalls, getChangelog, getJobs, getJob, createJob, abortJob, check, streamJob };
})();
window.API = API;
