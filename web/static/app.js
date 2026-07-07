// ---------------------------------------------------------
// حالت کلی و کمک‌کننده‌ها
// ---------------------------------------------------------
const state = {
  employees: [],
  branches: [],
  currentUser: null, // { employee_id, username, role, full_name }
};

function toast(msg, type = "") {
  const c = document.getElementById("toast-container");
  const el = document.createElement("div");
  el.className = "toast" + (type ? " " + type : "");
  el.textContent = msg;
  c.appendChild(el);
  requestAnimationFrame(() => el.classList.add("show"));
  setTimeout(() => {
    el.classList.remove("show");
    setTimeout(() => el.remove(), 250);
  }, 3200);
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  if (res.status === 401) {
    showLoginScreen();
    throw new Error("نشست شما منقضی شده؛ دوباره وارد شوید");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || "خطای ناشناخته");
  }
  return data;
}

function faDigits(s) {
  const map = ["۰","۱","۲","۳","۴","۵","۶","۷","۸","۹"];
  return String(s).replace(/[0-9]/g, d => map[d]);
}

function isAdmin() {
  return state.currentUser && state.currentUser.role === "admin";
}

// ---------------------------------------------------------
// ورود / خروج
// ---------------------------------------------------------
function showLoginScreen() {
  document.getElementById("login-screen").style.display = "flex";
  document.getElementById("app-root").style.display = "none";
}

function showAppScreen() {
  document.getElementById("login-screen").style.display = "none";
  document.getElementById("app-root").style.display = "block";
}

document.getElementById("login-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const username = document.getElementById("login-username").value.trim();
  const password = document.getElementById("login-password").value;
  const errEl = document.getElementById("login-error");
  errEl.style.display = "none";
  try {
    const user = await api("/api/auth/login", { method: "POST", body: JSON.stringify({ username, password }) });
    state.currentUser = user;
    document.getElementById("login-password").value = "";
    showAppScreen();
    applyRoleVisibility();
    await init();
  } catch (e) {
    errEl.textContent = e.message;
    errEl.style.display = "block";
  }
});

document.getElementById("logout-btn").addEventListener("click", async () => {
  try {
    await api("/api/auth/logout", { method: "POST" });
  } catch (e) { /* نادیده */ }
  state.currentUser = null;
  showLoginScreen();
});

function applyRoleVisibility() {
  const admin = isAdmin();
  document.querySelectorAll(".admin-only").forEach(el => {
    el.style.display = admin ? "" : "none";
  });
  document.getElementById("user-fullname").textContent = state.currentUser ? state.currentUser.full_name : "—";
  document.getElementById("branch-select-row").style.display = "";
  // کارمند عادی تب پیش‌فرضش «داشبورد» می‌ماند؛ تب «شیفت‌های من» فقط برای غیرمدیر معنی دارد.
  document.querySelector('[data-tab="my-shifts"]').style.display = admin ? "none" : "";
}

async function tryResumeSession() {
  try {
    const user = await api("/api/auth/me");
    state.currentUser = user;
    showAppScreen();
    applyRoleVisibility();
    await init();
  } catch (e) {
    showLoginScreen();
  }
}

// ---------------------------------------------------------
// تب‌ها
// ---------------------------------------------------------
document.querySelectorAll(".tab-btn").forEach(btn => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".tab-btn").forEach(b => b.classList.remove("active"));
    document.querySelectorAll(".tab-content").forEach(s => s.classList.remove("active"));
    btn.classList.add("active");
    document.getElementById("tab-" + btn.dataset.tab).classList.add("active");
    if (btn.dataset.tab === "reports") loadReportPreview();
    if (btn.dataset.tab === "manual") loadRecentShifts();
    if (btn.dataset.tab === "my-shifts") loadMyShifts();
    if (btn.dataset.tab === "branches") loadBranches();
  });
});

// ---------------------------------------------------------
// ساخت سلکت‌های تاریخ شمسی (سال/ماه/روز/ساعت/دقیقه)
// ---------------------------------------------------------
const PERSIAN_MONTHS = ["فروردین","اردیبهشت","خرداد","تیر","مرداد","شهریور","مهر","آبان","آذر","دی","بهمن","اسفند"];

function fillSelect(el, from, to, pad = false, labelFn = null) {
  el.innerHTML = "";
  for (let i = from; i <= to; i++) {
    const opt = document.createElement("option");
    opt.value = i;
    opt.textContent = labelFn ? labelFn(i) : faDigits(pad ? String(i).padStart(2, "0") : i);
    el.appendChild(opt);
  }
}

let currentJalaliYear = null;
let currentJalaliMonth = null;
let currentJalaliDay = null;
let dateSelectorsReady = false;

function setupDateSelectors(prefix) {
  const y = document.getElementById(prefix + "-year");
  const m = document.getElementById(prefix + "-month");
  const d = document.getElementById(prefix + "-day");
  const h = document.getElementById(prefix + "-hour");
  const mi = document.getElementById(prefix + "-minute");
  if (!y || !currentJalaliYear) return;
  fillSelect(y, currentJalaliYear - 3, currentJalaliYear + 2);
  fillSelect(m, 1, 12, false, (i) => faDigits(i) + " - " + PERSIAN_MONTHS[i - 1]);
  fillSelect(d, 1, 31, true);
  if (h) fillSelect(h, 0, 23, true);
  if (mi) fillSelect(mi, 0, 59, true);
  y.value = currentJalaliYear;
  m.value = currentJalaliMonth || 1;
  d.value = currentJalaliDay || 1;
  const now = new Date();
  if (h) h.value = String(now.getHours()).padStart(2, "0");
  if (mi) mi.value = String(now.getMinutes()).padStart(2, "0");
}

function syncJalaliToday(jy, jm, jd) {
  const yearChanged = jy !== currentJalaliYear;
  currentJalaliYear = jy;
  currentJalaliMonth = jm;
  currentJalaliDay = jd;
  if (!dateSelectorsReady || yearChanged) {
    ["in", "out", "cr-from", "cr-to"].forEach(setupDateSelectors);
    dateSelectorsReady = true;
  }
}

// ---------------------------------------------------------
// شعبه‌ها
// ---------------------------------------------------------
async function loadBranches() {
  try {
    state.branches = await api("/api/branches");
    renderBranchesTable();
    fillBranchSelects();
  } catch (e) {
    toast("خطا در دریافت شعبه‌ها: " + e.message, "error");
  }
}

function renderBranchesTable() {
  const tbody = document.querySelector("#branches-table tbody");
  if (!tbody) return;
  if (state.branches.length === 0) {
    tbody.innerHTML = `<tr><td colspan="4" class="empty-msg">هنوز شعبه‌ای اضافه نشده است.</td></tr>`;
    return;
  }
  tbody.innerHTML = state.branches.map(b => `
    <tr>
      <td>${faDigits(b.id)}</td>
      <td>${b.name}</td>
      <td>${b.active ? '<span class="badge badge-live">فعال</span>' : '<span class="badge badge-open">غیرفعال</span>'}</td>
      <td>
        <button class="btn btn-outline btn-sm" onclick="toggleBranch(${b.id}, ${!b.active})">
          ${b.active ? "غیرفعال کردن" : "فعال کردن"}
        </button>
      </td>
    </tr>
  `).join("");
}

function fillBranchSelects() {
  const activeBranches = state.branches.filter(b => b.active);
  const opts = activeBranches.map(b => `<option value="${b.id}">${b.name}</option>`).join("");
  const checkinSel = document.getElementById("checkin-branch");
  if (checkinSel) checkinSel.innerHTML = opts || `<option value="">— ابتدا شعبه اضافه کنید —</option>`;
  const manualSel = document.getElementById("manual-branch");
  if (manualSel) manualSel.innerHTML = opts;
}

async function toggleBranch(id, active) {
  try {
    await api("/api/branches/toggle", { method: "POST", body: JSON.stringify({ id, active }) });
    toast(active ? "شعبه فعال شد" : "شعبه غیرفعال شد", "success");
    await loadBranches();
  } catch (e) {
    toast(e.message, "error");
  }
}

document.getElementById("add-branch-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const name = document.getElementById("branch-name").value.trim();
  if (!name) return;
  try {
    await api("/api/branches", { method: "POST", body: JSON.stringify({ name }) });
    document.getElementById("branch-name").value = "";
    toast("شعبه اضافه شد ✅", "success");
    await loadBranches();
  } catch (e) {
    toast(e.message, "error");
  }
});

// ---------------------------------------------------------
// داشبورد
// ---------------------------------------------------------
async function loadDashboard() {
  try {
    if (isAdmin()) {
      const data = await api("/api/dashboard");
      document.getElementById("today-date").textContent = data.today;
      document.getElementById("dash-month").textContent = data.jalali_month;
      if (data.jalali_year) {
        syncJalaliToday(data.jalali_year, data.jalali_month_num, data.jalali_day);
      }

      const openList = document.getElementById("open-shifts-list");
      document.getElementById("open-count").textContent = faDigits(data.open_shifts.length);
      if (data.open_shifts.length === 0) {
        openList.innerHTML = `<div class="empty-msg">فعلاً کسی آنلاین نیست. 👍</div>`;
      } else {
        openList.innerHTML = data.open_shifts.map(s => `
          <div class="list-item">
            <div class="li-main">
              <span class="li-name">${s.employee_name}</span>
              <span class="li-meta">شعبه: ${s.branch_name || "—"} · ورود: ${s.check_in_jalali} · <span class="badge badge-open">آنلاین</span> ${s.manual ? '<span class="badge badge-manual">دستی</span>' : ''}</span>
            </div>
            <div class="li-actions">
              <button class="btn btn-danger btn-sm" onclick="doCheckoutAdmin(${s.employee_id})">ثبت خروج</button>
            </div>
          </div>
        `).join("");
      }

      const summaryEl = document.getElementById("month-summary");
      if (data.month_summary.length === 0) {
        summaryEl.innerHTML = `<div class="empty-msg">هنوز پرسنلی ثبت نشده است.</div>`;
      } else {
        summaryEl.innerHTML = data.month_summary.map(s => `
          <div class="summary-item">
            <div class="s-name">${s.employee.first_name} ${s.employee.last_name || ""}</div>
            <div class="s-hours">${faDigits(s.hours.toFixed(1))} <small>ساعت</small></div>
          </div>
        `).join("");
      }
    } else {
      // کارمند عادی: تاریخ امروز را از یک شیفت نمونه نمی‌گیریم؛ فقط وضعیت خودش را نشان می‌دهیم.
      document.getElementById("today-date").textContent = "";
    }
  } catch (e) {
    if (e.message && e.message.includes("منقضی")) return;
    toast("خطا در بارگذاری داشبورد: " + e.message, "error");
  }
  renderCheckinCard();
}

// برای کاربر عادی: فقط یک کارت مربوط به خودش، با شعبه‌ی انتخاب‌شده.
async function renderCheckinCard() {
  const grid = document.getElementById("checkin-list");
  if (!state.currentUser) return;
  const me = state.currentUser;
  grid.innerHTML = `
    <div class="checkin-card" id="checkin-card-${me.employee_id}">
      <span class="name">${me.full_name}</span>
      <span class="status" id="checkin-status-${me.employee_id}">در حال بررسی...</span>
      <button class="btn btn-primary btn-block btn-sm" id="checkin-btn-${me.employee_id}">...</button>
    </div>
  `;
  await refreshCheckinCard(me.employee_id);
}

async function refreshCheckinCard(employeeId) {
  try {
    const data = await api(`/api/shifts?employee_id=${employeeId}&range=all`);
    const openShift = data.items.find(s => s.open);
    const statusEl = document.getElementById(`checkin-status-${employeeId}`);
    const btn = document.getElementById(`checkin-btn-${employeeId}`);
    if (!statusEl || !btn) return;
    if (openShift) {
      statusEl.textContent = `از ${openShift.check_in_jalali} · شعبه: ${openShift.branch_name || "—"}`;
      btn.textContent = "ثبت خروج ⏹";
      btn.className = "btn btn-danger btn-block btn-sm";
      btn.onclick = () => doCheckout(employeeId);
      document.getElementById("checkin-branch").disabled = true;
    } else {
      statusEl.textContent = "بیرون از شیفت";
      btn.textContent = "ثبت ورود ▶";
      btn.className = "btn btn-success btn-block btn-sm";
      btn.onclick = () => doCheckin(employeeId);
      document.getElementById("checkin-branch").disabled = false;
    }
  } catch (e) { /* نادیده */ }
}

async function doCheckin(employeeId) {
  const branchSel = document.getElementById("checkin-branch");
  const branchId = parseInt(branchSel.value);
  if (!branchId) {
    toast("لطفاً اول شعبه را انتخاب کنید", "error");
    return;
  }
  try {
    await api("/api/checkin", { method: "POST", body: JSON.stringify({ employee_id: employeeId, branch_id: branchId }) });
    toast("ورود با موفقیت ثبت شد ✅", "success");
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
}

async function doCheckout(employeeId) {
  try {
    await api("/api/checkout", { method: "POST", body: JSON.stringify({ employee_id: employeeId }) });
    toast("خروج با موفقیت ثبت شد ✅", "success");
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
}

// مدیر می‌تواند از پنل «آنلاین‌های الان» برای هرکسی خروج بزند.
async function doCheckoutAdmin(employeeId) {
  try {
    await api("/api/checkout", { method: "POST", body: JSON.stringify({ employee_id: employeeId }) });
    toast("خروج ثبت شد ✅", "success");
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
}

// ---------------------------------------------------------
// پرسنل (فقط مدیر)
// ---------------------------------------------------------
async function loadEmployees() {
  try {
    state.employees = await api("/api/employees");
    renderEmployeesTable();
    fillEmployeeSelects();
  } catch (e) {
    toast("خطا در دریافت پرسنل: " + e.message, "error");
  }
}

function renderEmployeesTable() {
  const tbody = document.querySelector("#employees-table tbody");
  if (!tbody) return;
  if (state.employees.length === 0) {
    tbody.innerHTML = `<tr><td colspan="6" class="empty-msg">هنوز نفری اضافه نشده است.</td></tr>`;
    return;
  }
  tbody.innerHTML = state.employees.map(e => `
    <tr>
      <td>${faDigits(e.id)}</td>
      <td>${e.first_name} ${e.last_name || ""}</td>
      <td>${e.active ? '<span class="badge badge-live">فعال</span>' : '<span class="badge badge-open">غیرفعال</span>'}</td>
      <td>${e.has_account ? e.username : '<span class="hint">بدون حساب</span>'}</td>
      <td>${e.has_account ? (e.role === "admin" ? "مدیر" : "کارمند") : "—"}</td>
      <td>
        <button class="btn btn-outline btn-sm" onclick="toggleEmployee(${e.id}, ${!e.active})">
          ${e.active ? "غیرفعال کردن" : "فعال کردن"}
        </button>
        <button class="btn btn-outline btn-sm" onclick="openCredentialsForm(${e.id}, '${(e.first_name + " " + (e.last_name||"")).replace(/'/g, "")}', '${e.username||""}', '${e.role||"employee"}')">
          ${e.has_account ? "ویرایش حساب" : "ساخت حساب ورود"}
        </button>
      </td>
    </tr>
  `).join("");
}

function fillEmployeeSelects() {
  const opts = state.employees.map(e => `<option value="${e.id}">${e.first_name} ${e.last_name || ""}</option>`).join("");
  const manualSel = document.getElementById("manual-employee");
  if (manualSel) manualSel.innerHTML = opts;
  const exportSel = document.getElementById("export-employee");
  if (exportSel) exportSel.innerHTML = `<option value="0">همه پرسنل</option>` + opts;
}

async function toggleEmployee(id, active) {
  try {
    await api("/api/employees/toggle", { method: "POST", body: JSON.stringify({ id, active }) });
    toast(active ? "فرد فعال شد" : "فرد غیرفعال شد", "success");
    await loadEmployees();
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
}

document.getElementById("add-employee-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const first = document.getElementById("emp-first").value.trim();
  const last = document.getElementById("emp-last").value.trim();
  if (!first) return;
  try {
    await api("/api/employees", { method: "POST", body: JSON.stringify({ first_name: first, last_name: last }) });
    document.getElementById("emp-first").value = "";
    document.getElementById("emp-last").value = "";
    toast("نفر جدید اضافه شد ✅", "success");
    await loadEmployees();
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
});

// ---------------------------------------------------------
// حساب کاربری (username/password/role)
// ---------------------------------------------------------
function openCredentialsForm(employeeId, fullName, username, role) {
  document.getElementById("credentials-card").style.display = "block";
  document.getElementById("cred-emp-name").textContent = fullName;
  document.getElementById("cred-employee-id").value = employeeId;
  document.getElementById("cred-username").value = username || "";
  document.getElementById("cred-password").value = "";
  document.getElementById("cred-role").value = role || "employee";
  document.getElementById("credentials-card").scrollIntoView({ behavior: "smooth", block: "center" });
}

document.getElementById("cred-cancel").addEventListener("click", () => {
  document.getElementById("credentials-card").style.display = "none";
});

document.getElementById("credentials-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const body = {
    employee_id: parseInt(document.getElementById("cred-employee-id").value),
    username: document.getElementById("cred-username").value.trim(),
    password: document.getElementById("cred-password").value,
    role: document.getElementById("cred-role").value,
  };
  try {
    await api("/api/employees/credentials", { method: "POST", body: JSON.stringify(body) });
    toast("حساب کاربری ذخیره شد ✅", "success");
    document.getElementById("credentials-card").style.display = "none";
    await loadEmployees();
  } catch (e) {
    toast(e.message, "error");
  }
});

// ---------------------------------------------------------
// ثبت دستی (فقط مدیر)
// ---------------------------------------------------------
document.getElementById("has-out").addEventListener("change", (ev) => {
  document.getElementById("out-fieldset").style.display = ev.target.checked ? "block" : "none";
});

document.getElementById("manual-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hasOut = document.getElementById("has-out").checked;
  const body = {
    employee_id: parseInt(document.getElementById("manual-employee").value),
    branch_id: parseInt(document.getElementById("manual-branch").value),
    in_year: parseInt(document.getElementById("in-year").value),
    in_month: parseInt(document.getElementById("in-month").value),
    in_day: parseInt(document.getElementById("in-day").value),
    in_hour: parseInt(document.getElementById("in-hour").value),
    in_minute: parseInt(document.getElementById("in-minute").value),
    has_out: hasOut,
    note: document.getElementById("manual-note").value.trim(),
  };
  if (!body.branch_id) {
    toast("لطفاً شعبه را انتخاب کنید", "error");
    return;
  }
  if (hasOut) {
    body.out_year = parseInt(document.getElementById("out-year").value);
    body.out_month = parseInt(document.getElementById("out-month").value);
    body.out_day = parseInt(document.getElementById("out-day").value);
    body.out_hour = parseInt(document.getElementById("out-hour").value);
    body.out_minute = parseInt(document.getElementById("out-minute").value);
  }
  try {
    await api("/api/shifts/manual", { method: "POST", body: JSON.stringify(body) });
    toast("شیفت دستی ثبت شد ✅", "success");
    document.getElementById("manual-note").value = "";
    loadRecentShifts();
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
});

async function loadRecentShifts() {
  try {
    const data = await api("/api/shifts?range=all&page=1&page_size=15");
    const recent = data.items;
    const list = document.getElementById("recent-shifts");
    if (recent.length === 0) {
      list.innerHTML = `<div class="empty-msg">هنوز شیفتی ثبت نشده است.</div>`;
      return;
    }
    list.innerHTML = recent.map(s => `
      <div class="list-item">
        <div class="li-main">
          <span class="li-name">${s.employee_name} ${s.manual ? '<span class="badge badge-manual">دستی</span>' : '<span class="badge badge-live">زنده</span>'} ${s.open ? '<span class="badge badge-open">باز</span>' : ''}</span>
          <span class="li-meta">شعبه: ${s.branch_name || "—"} · ورود: ${s.check_in_jalali} ${s.check_out_jalali ? "· خروج: " + s.check_out_jalali : ""} · مدت: ${faDigits(s.hours.toFixed(1))} ساعت ${s.note ? "· " + s.note : ""}</span>
        </div>
        <div class="li-actions">
          <button class="btn btn-outline btn-sm" onclick="deleteShift(${s.id})">حذف</button>
        </div>
      </div>
    `).join("");
  } catch (e) {
    toast("خطا در بارگذاری شیفت‌ها: " + e.message, "error");
  }
}

async function deleteShift(id) {
  if (!confirm("آیا از حذف این شیفت مطمئن هستید؟")) return;
  try {
    await api("/api/shifts/delete", { method: "POST", body: JSON.stringify({ id }) });
    toast("شیفت حذف شد", "success");
    loadRecentShifts();
    loadDashboard();
  } catch (e) {
    toast(e.message, "error");
  }
}

// ---------------------------------------------------------
// شیفت‌های من (کارمند عادی)
// ---------------------------------------------------------
async function loadMyShifts() {
  try {
    const data = await api("/api/shifts?range=all&page=1&page_size=50");
    const tbody = document.querySelector("#my-shifts-table tbody");
    if (data.items.length === 0) {
      tbody.innerHTML = `<tr><td colspan="4" class="empty-msg">هنوز شیفتی برای شما ثبت نشده است.</td></tr>`;
      return;
    }
    tbody.innerHTML = data.items.map(s => `
      <tr>
        <td>${s.branch_name || "—"}</td>
        <td>${s.check_in_jalali}</td>
        <td>${s.check_out_jalali || "شیفت باز"}</td>
        <td>${faDigits(s.hours.toFixed(2))}</td>
      </tr>
    `).join("");
  } catch (e) {
    toast("خطا در بارگذاری شیفت‌های شما: " + e.message, "error");
  }
}

// ---------------------------------------------------------
// گزارش‌ها (فقط مدیر)
// ---------------------------------------------------------
const REPORT_PAGE_SIZE = 20;

document.getElementById("export-range").addEventListener("change", (ev) => {
  document.getElementById("custom-range").style.display = ev.target.value === "custom" ? "flex" : "none";
  loadReportPreview(1);
});
document.getElementById("export-employee").addEventListener("change", () => loadReportPreview(1));
document.querySelectorAll("#custom-range select").forEach(sel => {
  sel.addEventListener("change", () => loadReportPreview(1));
});

function buildQueryFromReportForm(page) {
  const range = document.getElementById("export-range").value;
  const empId = document.getElementById("export-employee").value;
  const params = new URLSearchParams({ range, employee_id: empId });
  if (range === "custom") {
    params.set("from_year", document.getElementById("cr-from-year").value);
    params.set("from_month", document.getElementById("cr-from-month").value);
    params.set("from_day", document.getElementById("cr-from-day").value);
    params.set("to_year", document.getElementById("cr-to-year").value);
    params.set("to_month", document.getElementById("cr-to-month").value);
    params.set("to_day", document.getElementById("cr-to-day").value);
  }
  if (page) {
    params.set("page", page);
    params.set("page_size", REPORT_PAGE_SIZE);
  }
  return params;
}

async function loadReportPreview(page = 1) {
  try {
    const params = buildQueryFromReportForm(page);
    const data = await api("/api/shifts?" + params.toString());
    const shifts = data.items;
    const tbody = document.querySelector("#report-table tbody");
    if (shifts.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty-msg">شیفتی در این بازه پیدا نشد.</td></tr>`;
    } else {
      tbody.innerHTML = shifts.map(s => `
        <tr>
          <td>${s.employee_name}</td>
          <td>${s.branch_name || "—"}</td>
          <td>${s.check_in_jalali}</td>
          <td>${s.check_out_jalali || "شیفت باز"}</td>
          <td>${faDigits(s.hours.toFixed(2))}</td>
          <td>${s.manual ? "دستی" : "زنده"}</td>
          <td>${s.note || "—"}</td>
        </tr>
      `).join("");
    }
    renderReportPagination(data);
  } catch (e) {
    toast("خطا در دریافت گزارش: " + e.message, "error");
  }
}

function renderReportPagination(data) {
  const el = document.getElementById("report-pagination");
  if (!el) return;
  if (!data.total) {
    el.innerHTML = "";
    return;
  }
  el.innerHTML = `
    <button type="button" class="btn btn-outline btn-sm" id="report-prev" ${data.page <= 1 ? "disabled" : ""}>‹ قبلی</button>
    <span class="page-info">صفحه ${faDigits(data.page)} از ${faDigits(data.total_pages)} · مجموع ${faDigits(data.total)} رکورد</span>
    <button type="button" class="btn btn-outline btn-sm" id="report-next" ${data.page >= data.total_pages ? "disabled" : ""}>بعدی ›</button>
  `;
  const prevBtn = document.getElementById("report-prev");
  const nextBtn = document.getElementById("report-next");
  if (prevBtn) prevBtn.addEventListener("click", () => loadReportPreview(data.page - 1));
  if (nextBtn) nextBtn.addEventListener("click", () => loadReportPreview(data.page + 1));
}

document.getElementById("export-form").addEventListener("submit", (ev) => {
  ev.preventDefault();
  const params = buildQueryFromReportForm();
  window.location.href = "/api/export?" + params.toString();
});

// ---------------------------------------------------------
// شروع برنامه
// ---------------------------------------------------------
async function init() {
  await loadBranches();
  if (isAdmin()) {
    await loadEmployees();
  }
  await loadDashboard();
  if (!isAdmin()) {
    await loadMyShifts();
  }
  if (window._dashboardInterval) clearInterval(window._dashboardInterval);
  window._dashboardInterval = setInterval(loadDashboard, 30000);
}

// شروع: اول ببین آیا session معتبر داریم یا باید صفحه‌ی لاگین را نشان بدهیم.
tryResumeSession();
