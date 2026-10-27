// TaskFlow UI. Plain JavaScript, no build step.
// User data is only ever written with textContent / value, never innerHTML, so task titles cannot inject HTML.
"use strict";

const API = "/api/v1/tasks";

const els = {
  form: document.getElementById("create-form"),
  title: document.getElementById("title"),
  description: document.getElementById("description"),
  list: document.getElementById("tasks"),
  empty: document.getElementById("empty"),
  error: document.getElementById("error"),
  refresh: document.getElementById("refresh"),
  template: document.getElementById("task-template"),
};

class ApiError extends Error {
  constructor(status, body, requestId) {
    super(body?.error || `HTTP ${status}`);
    this.status = status;
    this.fields = body?.fields || {};
    this.requestId = requestId;
  }
}

async function api(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const requestId = res.headers.get("X-Request-ID");
  const data = res.status === 204 ? null : await res.json().catch(() => null);
  if (!res.ok) throw new ApiError(res.status, data, requestId);
  return data;
}

function showError(err) {
  // The request ID lets an operator find the matching nginx and API log lines.
  const ref = err.requestId ? ` (request ID ${err.requestId})` : "";
  els.error.textContent = `${err.message}${ref}`;
  els.error.hidden = false;
}

function clearError() {
  els.error.hidden = true;
  els.error.textContent = "";
}

function showFieldErrors(fields) {
  for (const p of els.form.querySelectorAll(".field-error")) {
    p.textContent = fields[p.dataset.field] || "";
  }
}

function renderTask(task) {
  const li = els.template.content.firstElementChild.cloneNode(true);
  li.dataset.id = task.id;
  li.querySelector(".task-title").textContent = task.title;
  const desc = li.querySelector(".task-description");
  desc.textContent = task.description;
  desc.hidden = task.description === "";

  const status = li.querySelector(".task-status");
  status.value = task.status;
  status.id = `status-${task.id}`;
  li.querySelector("label").htmlFor = status.id;
  li.classList.toggle("done", task.status === "done");
  status.addEventListener("change", () => updateStatus(task.id, status.value));

  li.querySelector(".task-delete").addEventListener("click", () => deleteTask(task.id, task.title));
  return li;
}

async function loadTasks() {
  try {
    const page = await api("GET", `${API}?limit=100`);
    els.list.replaceChildren(...page.items.map(renderTask));
    els.empty.hidden = page.items.length > 0;
    clearError();
  } catch (err) {
    showError(err);
  }
}

async function createTask(event) {
  event.preventDefault();
  showFieldErrors({});
  try {
    await api("POST", API, { title: els.title.value, description: els.description.value });
    els.form.reset();
    els.title.focus();
    await loadTasks();
  } catch (err) {
    if (err.status === 400 && Object.keys(err.fields).length > 0) {
      showFieldErrors(err.fields);
    } else {
      showError(err);
    }
  }
}

async function updateStatus(id, status) {
  try {
    await api("PATCH", `${API}/${id}`, { status });
  } catch (err) {
    showError(err);
  }
  await loadTasks();
}

async function deleteTask(id, title) {
  if (!confirm(`Delete "${title}"?`)) return;
  try {
    await api("DELETE", `${API}/${id}`);
  } catch (err) {
    showError(err);
  }
  await loadTasks();
}

els.form.addEventListener("submit", createTask);
els.refresh.addEventListener("click", loadTasks);
loadTasks();
