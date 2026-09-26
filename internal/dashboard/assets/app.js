// xmail dashboard — account management UI (PRD.MD §6.7, PLAN-DASHBOARD.md).
//
// Talks only to the REST API in internal/api (docs/API.md): no business
// logic lives here, every action is an existing endpoint call.
//
// Served with CSP `default-src 'self'`, so no inline handlers, no
// inline styles, no CDN. Server/user data is never written with
// innerHTML — only textContent and DOM nodes (PLAN-DASHBOARD.md §4.4).

const KEY_STORAGE = 'xmail_api_key';
const PROTOCOLS = ['smtp', 'imap', 'pop3'];

let apiKey = sessionStorage.getItem(KEY_STORAGE) || '';
let flash = null; // one-shot banner {type:'ok'|'err', text} consumed by the next render

const appEl = document.getElementById('app');
const topbarEl = document.getElementById('topbar');
const sessionEl = document.getElementById('session-indicator');

/* ------------------------------ helpers ------------------------------ */

function el(tag, props = {}, children = []) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key in node) node[key] = value; // value, checked, disabled, hidden, selected, type, ...
    else if (value === true) node.setAttribute(key, '');
    else if (value !== false && value != null) node.setAttribute(key, String(value));
  }
  for (const child of [].concat(children)) {
    if (child == null || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

function clear(node) {
  node.replaceChildren();
}

function fmtError(err) {
  return err && err.message ? err.message : String(err);
}

class ApiError extends Error {
  constructor(code, message, status) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

/* ---------------------------- API client ----------------------------- */

async function api(method, path, body) {
  const headers = {};
  if (apiKey) headers['X-API-Key'] = apiKey;
  const init = { method, headers };
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }

  let res;
  try {
    res = await fetch(path, init);
  } catch (err) {
    throw new ApiError('network_error', `Tidak bisa menghubungi server: ${fmtError(err)}`, 0);
  }

  let payload = null;
  try {
    payload = await res.json();
  } catch {
    // Non-JSON body (e.g. the file server's 404 page).
  }

  if (!res.ok || (payload && payload.error)) {
    const code = (payload && payload.error && payload.error.code) || String(res.status);
    const message = (payload && payload.error && payload.error.message) || res.statusText || 'Permintaan gagal';
    throw new ApiError(code, message, res.status);
  }
  return payload ? payload.data : null;
}

/* ----------------------------- scaffolding --------------------------- */

function field(labelText, input, hintText) {
  const wrap = el('div');
  wrap.append(el('label', input.id ? { for: input.id } : {}, [labelText]));
  wrap.append(input);
  if (hintText) wrap.append(el('div', { class: 'hint' }, [hintText]));
  return wrap;
}

function takeFlash() {
  if (!flash) return null;
  const node = el('div', { class: `banner banner-${flash.type}` }, [flash.text]);
  flash = null;
  return node;
}

function updateSessionIndicator() {
  sessionEl.textContent = apiKey ? 'sesi aktif' : '';
}

function renderFatal(title, message) {
  const card = el('div', { class: 'card stack' });
  card.append(el('h1', {}, [title]), el('p', { class: 'error' }, [message]));
  const back = el('button', { type: 'button', class: 'btn' }, ['Kembali ke daftar']);
  back.addEventListener('click', () => navigate('#/accounts'));
  card.append(el('div', { class: 'form-actions' }, [back]));
  appEl.append(card);
}

/* ------------------------------- views ------------------------------- */

function renderLogin(message) {
  topbarEl.hidden = true;
  clear(appEl);

  const form = el('form', { class: 'card stack' });
  form.append(el('h1', {}, ['Masuk']));
  form.append(el('p', { class: 'muted' }, ['Masukkan API key xmail (nilai XMAIL_API_KEY di server).']));

  const errorEl = el('p', { class: 'error', hidden: !message });
  if (message) errorEl.textContent = message;

  const input = el('input', { type: 'password', id: 'api-key', autocomplete: 'current-password', placeholder: 'X-API-Key' });
  const submit = el('button', { type: 'submit', class: 'btn btn-primary' }, ['Masuk']);

  form.append(errorEl, field('API key', input), el('div', { class: 'form-actions' }, [submit]));

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const candidate = input.value.trim();
    if (!candidate) return;
    submit.disabled = true;
    const previous = apiKey;
    apiKey = candidate;
    try {
      await api('GET', '/accounts'); // cheap way to prove the key works
      sessionStorage.setItem(KEY_STORAGE, candidate);
      updateSessionIndicator();
      navigate('#/accounts');
    } catch (err) {
      apiKey = previous;
      errorEl.textContent = err.code === 'unauthorized'
        ? 'API key ditolak (401).'
        : `Gagal memverifikasi API key: ${fmtError(err)}`;
      errorEl.hidden = false;
      submit.disabled = false;
    }
  });

  appEl.append(form);
  input.focus();
}

async function renderList() {
  topbarEl.hidden = false;
  updateSessionIndicator();
  clear(appEl);
  appEl.append(el('p', { class: 'muted' }, ['Memuat akun…']));

  let accounts;
  try {
    accounts = await api('GET', '/accounts');
  } catch (err) {
    if (err.code === 'unauthorized') return forceLogout('API key ditolak — silakan masuk lagi.');
    clear(appEl);
    const banner = takeFlash();
    if (banner) appEl.append(banner);
    renderFatal('Gagal memuat akun', fmtError(err));
    return;
  }

  clear(appEl);
  const banner = takeFlash();
  if (banner) appEl.append(banner);

  const header = el('div', { class: 'row row-between' });
  header.append(el('h1', {}, [`Akun (${accounts.length})`]));
  const addBtn = el('button', { type: 'button', class: 'btn btn-primary' }, ['+ Tambah akun']);
  addBtn.addEventListener('click', () => navigate('#/accounts/new'));
  header.append(addBtn);
  appEl.append(header);

  if (accounts.length === 0) {
    appEl.append(el('p', { class: 'muted' }, ['Belum ada akun. Klik "Tambah akun" untuk mulai.']));
    return;
  }

  const headRow = el('tr');
  for (const label of ['Nama', 'Email', 'Username', 'Protokol', 'Aksi']) headRow.append(el('th', {}, [label]));

  const tbody = el('tbody');
  for (const account of accounts) tbody.append(accountRow(account));

  appEl.append(el('table', { class: 'accounts' }, [el('thead', {}, [headRow]), tbody]));
}

async function renderForm(id) {
  topbarEl.hidden = false;
  updateSessionIndicator();
  clear(appEl);
  appEl.append(el('p', { class: 'muted' }, ['Memuat…']));

  let account = null;
  if (id) {
    try {
      account = await api('GET', `/accounts/${encodeURIComponent(id)}`);
    } catch (err) {
      if (err.code === 'unauthorized') return forceLogout('API key ditolak — silakan masuk lagi.');
      clear(appEl);
      const banner = takeFlash();
      if (banner) appEl.append(banner);
      renderFatal('Gagal memuat akun', fmtError(err));
      return;
    }
  }

  clear(appEl);
  const isEdit = Boolean(id);
  const banner = takeFlash();
  if (banner) appEl.append(banner);

  const nameInput = el('input', { type: 'text', id: 'name', value: account ? account.name : '' });
  const emailInput = el('input', { type: 'email', id: 'email', value: account ? account.email : '' });
  const usernameInput = el('input', { type: 'text', id: 'username', value: account ? account.username : '' });
  const passwordInput = el('input', { type: 'password', id: 'password', autocomplete: 'new-password' });

  const form = el('form', { class: 'card stack' });
  form.append(el('h1', {}, [isEdit ? 'Edit akun' : 'Tambah akun']));
  form.append(
    field('Nama', nameInput),
    field('Email (jadi alamat From saat kirim)', emailInput),
    field('Username', usernameInput),
    field(
      'Password',
      passwordInput,
      isEdit
        ? 'Kosongkan untuk mempertahankan kredensial tersimpan. Password tidak pernah ditampilkan oleh API.'
        : 'Wajib diisi saat membuat akun.',
    ),
  );

  const sections = {};
  for (const proto of PROTOCOLS) {
    const section = protocolSection(proto, account ? account[proto] : null);
    sections[proto] = section;
    form.append(section.node);
  }

  const errorEl = el('p', { class: 'error', hidden: true });
  form.append(errorEl);

  // Connectivity checks hit the *saved* config (test-connection reads the
  // account from the DB), so this belongs only in edit mode.
  if (isEdit) form.append(testPanel(account));

  const saveBtn = el('button', { type: 'submit', class: 'btn btn-primary' }, [isEdit ? 'Simpan perubahan' : 'Simpan akun']);
  const cancelBtn = el('button', { type: 'button', class: 'btn' }, ['Batal']);
  cancelBtn.addEventListener('click', () => navigate('#/accounts'));
  form.append(el('div', { class: 'form-actions' }, [saveBtn, cancelBtn]));

  const showError = (message) => {
    errorEl.textContent = message;
    errorEl.hidden = false;
  };

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    errorEl.hidden = true;

    const payload = {
      name: nameInput.value.trim(),
      email: emailInput.value.trim(),
      username: usernameInput.value.trim(),
      smtp: sections.smtp.read(),
      imap: sections.imap.read(),
      pop3: sections.pop3.read(),
    };

    // Mirror the server's validation for fast feedback; the server stays
    // the authority — its error.message is shown verbatim below.
    const problem = validate(payload, { isEdit, password: passwordInput.value });
    if (problem) return showError(problem);

    if (isEdit) {
      // nil password = leave the stored credential unchanged (docs/API.md §7.5).
      payload.password = passwordInput.value ? passwordInput.value : null;
    } else {
      payload.password = passwordInput.value;
    }

    saveBtn.disabled = true;
    try {
      const saved = isEdit
        ? await api('PUT', `/accounts/${encodeURIComponent(id)}`, payload)
        : await api('POST', '/accounts', payload);
      flash = {
        type: 'ok',
        text: isEdit ? 'Perubahan disimpan.' : 'Akun dibuat. Uji konektivitasnya di panel bawah.',
      };
      navigate(`#/accounts/${encodeURIComponent(saved.id)}`);
    } catch (err) {
      if (err.code === 'unauthorized') return forceLogout('API key ditolak — silakan masuk lagi.');
      showError(fmtError(err));
      saveBtn.disabled = false;
    }
  });

  appEl.append(form);
  nameInput.focus();
}

/* ---------------------------- list pieces ---------------------------- */

function configuredProtocols(account) {
  return PROTOCOLS.filter((proto) => account[proto] != null);
}

function protocolBadges(account) {
  const list = configuredProtocols(account);
  if (list.length === 0) return el('span', { class: 'muted' }, ['—']);
  const wrap = el('span');
  for (const proto of list) wrap.append(el('span', { class: 'badge' }, [proto]));
  return wrap;
}

function accountRow(account) {
  const testBtn = el('button', { type: 'button', class: 'btn btn-sm' }, ['Test']);
  const editBtn = el('button', { type: 'button', class: 'btn btn-sm' }, ['Edit']);
  const delBtn = el('button', { type: 'button', class: 'btn btn-sm btn-danger' }, ['Hapus']);

  const resultsRow = el('tr', { class: 'results', hidden: true });
  const resultsCell = el('td', { colspan: '5' });
  resultsRow.append(resultsCell);

  testBtn.addEventListener('click', async () => {
    resultsRow.hidden = false;
    testBtn.disabled = true;
    await runTests(account, resultsCell);
    testBtn.disabled = false;
  });
  editBtn.addEventListener('click', () => navigate(`#/accounts/${encodeURIComponent(account.id)}`));
  delBtn.addEventListener('click', () => {
    void deleteAccount(account, delBtn);
  });

  const row = el('tr', {}, [
    el('td', {}, [account.name]),
    el('td', {}, [account.email]),
    el('td', {}, [account.username]),
    el('td', {}, [protocolBadges(account)]),
    el('td', { class: 'actions' }, [testBtn, editBtn, delBtn]),
  ]);

  const frag = document.createDocumentFragment();
  frag.append(row, resultsRow);
  return frag;
}

async function runTests(account, resultsEl) {
  clear(resultsEl);
  const list = configuredProtocols(account);
  if (list.length === 0) {
    resultsEl.append(el('span', { class: 'muted' }, ['Tidak ada protokol terkonfigurasi.']));
    return;
  }
  for (const proto of list) {
    const badge = el('span', { class: 'badge badge-pending' }, [`${proto}: menguji…`]);
    resultsEl.append(badge);
    await ping(account.id, proto, badge);
  }
}

async function ping(accountId, proto, badge) {
  try {
    await api('POST', `/accounts/${encodeURIComponent(accountId)}/test-connection`, { protocol: proto });
    badge.className = 'badge badge-ok';
    badge.textContent = `${proto}: ok`;
  } catch (err) {
    badge.className = 'badge badge-err';
    badge.textContent = `${proto}: gagal — ${fmtError(err)}`;
  }
}

async function deleteAccount(account, button) {
  if (!window.confirm(`Hapus akun "${account.name}"? Kredensial dan cache-nya ikut terhapus.`)) return;
  button.disabled = true;
  try {
    await api('DELETE', `/accounts/${encodeURIComponent(account.id)}`);
    flash = { type: 'ok', text: `Akun "${account.name}" dihapus.` };
  } catch (err) {
    flash = { type: 'err', text: `Gagal menghapus akun "${account.name}": ${fmtError(err)}` };
  }
  route();
}

/* ---------------------------- form pieces ---------------------------- */

function protocolSection(proto, cfg) {
  const enabled = el('input', { type: 'checkbox', id: `${proto}-enabled`, checked: Boolean(cfg) });
  const host = el('input', { type: 'text', id: `${proto}-host`, placeholder: 'host', value: cfg ? cfg.host : '' });
  const port = el('input', {
    type: 'number',
    id: `${proto}-port`,
    min: '1',
    max: '65535',
    placeholder: 'port',
    value: cfg && cfg.port ? String(cfg.port) : '',
  });
  const tls = el('select', { id: `${proto}-tls` });
  for (const mode of tlsModesFor(proto)) {
    const option = el('option', { value: mode }, [mode]);
    if ((cfg ? cfg.tls_mode : 'tls') === mode) option.selected = true;
    tls.append(option);
  }

  const node = el('div', { class: 'protocol' }, [
    el('div', { class: 'checkbox-row' }, [enabled, el('label', { for: `${proto}-enabled` }, [proto.toUpperCase()])]),
    el('div', { class: 'grid-3' }, [field('Host', host), field('Port', port), field('TLS mode', tls)]),
  ]);

  const inputs = [host, port, tls];
  const sync = () => {
    for (const input of inputs) input.disabled = !enabled.checked;
  };
  enabled.addEventListener('change', sync);
  sync();

  return {
    node,
    read() {
      if (!enabled.checked) return null;
      return { host: host.value.trim(), port: Number(port.value), tls_mode: tls.value };
    },
  };
}

// POP3 has no STARTTLS: mailer/pop3 rejects it and account.Validate refuses
// to save it (docs/API.md §6), so don't offer a value that can only fail.
function tlsModesFor(proto) {
  return proto === 'pop3' ? ['tls', 'none'] : ['tls', 'starttls', 'none'];
}

function testPanel(account) {
  const section = el('div', { class: 'protocol' });
  section.append(el('h2', {}, ['Uji koneksi']));
  section.append(el('p', { class: 'hint' }, ['Menguji konfigurasi yang tersimpan di server, bukan perubahan yang belum disimpan.']));

  const list = configuredProtocols(account);
  if (list.length === 0) {
    section.append(el('p', { class: 'muted' }, ['Akun ini belum punya protokol terkonfigurasi.']));
    return section;
  }

  const results = el('div', { class: 'results-list' });
  const buttons = el('div', { class: 'form-actions' });
  for (const proto of list) {
    const btn = el('button', { type: 'button', class: 'btn btn-sm' }, [`Test ${proto}`]);
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      const badge = el('span', { class: 'badge badge-pending' }, [`${proto}: menguji…`]);
      results.append(badge);
      await ping(account.id, proto, badge);
      btn.disabled = false;
    });
    buttons.append(btn);
  }
  section.append(buttons, results);
  return section;
}

function validate(payload, { isEdit, password }) {
  if (!payload.name) return 'Nama wajib diisi.';
  if (!payload.email) return 'Email wajib diisi.';
  if (!payload.username) return 'Username wajib diisi.';
  if (!isEdit && !password) return 'Password wajib diisi saat membuat akun.';

  const configured = PROTOCOLS.filter((proto) => payload[proto] != null);
  if (configured.length === 0) return 'Aktifkan minimal satu protokol (SMTP/IMAP/POP3).';

  for (const proto of configured) {
    const cfg = payload[proto];
    if (!cfg.host) return `Host ${proto.toUpperCase()} wajib diisi.`;
    if (!Number.isInteger(cfg.port) || cfg.port < 1 || cfg.port > 65535) {
      return `Port ${proto.toUpperCase()} harus bilangan bulat 1–65535.`;
    }
  }
  return null;
}

/* ------------------------------- router ------------------------------ */

function forceLogout(message) {
  apiKey = '';
  sessionStorage.removeItem(KEY_STORAGE);
  flash = null;
  renderLogin(message || null);
}

function navigate(hash) {
  if (location.hash === hash) {
    route();
    return;
  }
  location.hash = hash; // fires hashchange -> route()
}

function route() {
  if (!apiKey) {
    renderLogin();
    return;
  }
  const hash = location.hash || '#/accounts';
  if (hash === '#/accounts') return void renderList();
  if (hash === '#/accounts/new') return void renderForm(null);

  const match = hash.match(/^#\/accounts\/(.+)$/);
  if (match) return void renderForm(decodeURIComponent(match[1]));

  navigate('#/accounts');
}

document.getElementById('logout').addEventListener('click', () => forceLogout(null));
window.addEventListener('hashchange', route);
route();
