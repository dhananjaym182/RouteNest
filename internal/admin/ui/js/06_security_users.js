// ========== 管理员用户与角色 ==========
let _adminUsers = [];
let _editingAdminUser = null;
const ADMIN_ASSIGNABLE_ROLES = ['viewer', 'provider_admin', 'credential_admin', 'security_admin'];

async function loadAdminUsers() {
  const host = _('adminUsersList');
  if (!host) return;
  try {
    const response = await api('GET', '/auth/users');
    const users = response.data && response.data.users;
    if (!Array.isArray(users)) throw new Error(t('管理员用户响应无效'));
    _adminUsers = users.slice().sort((a, b) => String(a.username || '').localeCompare(String(b.username || '')));
    host.textContent = '';
    if (!_adminUsers.length) {
      const empty = document.createElement('div'); empty.className = 'access-empty'; empty.textContent = t('尚未配置管理员用户'); host.appendChild(empty); return;
    }
    _adminUsers.forEach(user => {
      const row = document.createElement('div'); row.className = 'access-list-row';
      const info = document.createElement('div'); info.className = 'access-row-info';
      const heading = document.createElement('div'); heading.className = 'access-row-title';
      const title = document.createElement('strong'); title.textContent = user.username || user.id;
      const kind = document.createElement('span'); kind.className = 'access-badge' + (user.disabled ? ' is-warning' : '');
      kind.textContent = user.disabled ? t('已停用') : (user.provider ? String(user.provider) : t('本地账号'));
      const details = document.createElement('div'); details.className = 'access-row-meta access-role-badges';
      renderAdminRoleBadges(details, user.roles);
      heading.appendChild(title); heading.appendChild(kind); info.appendChild(heading); info.appendChild(details); row.appendChild(info);
      const actions = document.createElement('div'); actions.className = 'access-row-actions';
      const immutable = user.id === 'break-glass' || (_adminPrincipal && user.id === _adminPrincipal.userID);
      if (!immutable) {
        const edit = document.createElement('button'); edit.type = 'button'; edit.className = 'btn btn-sm'; edit.textContent = t('编辑');
        edit.addEventListener('click', () => openAdminUserEditor(user.id)); actions.appendChild(edit);
      }
      if (!user.provider && !immutable) {
        const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'btn btn-sm btn-danger'; remove.textContent = t('删除');
        remove.addEventListener('click', () => deleteAdminUser(user.id, remove)); actions.appendChild(remove);
      }
      row.appendChild(actions); host.appendChild(row);
    });
  } catch (e) {
    host.textContent = '';
    const error = document.createElement('div'); error.className = 'error-box'; error.textContent = e.message; host.appendChild(error);
  }
}

function adminUserSelectedRoles() {
  return Array.from(document.querySelectorAll('#adminUserEditor .admin-user-roles input:checked')).map(input => input.value);
}

function setAdminUserRoles(roles, disabled) {
  const selected = new Set(roles || []);
  document.querySelectorAll('#adminUserEditor .admin-user-roles input').forEach(input => {
    input.checked = selected.has(input.value); input.disabled = !!disabled;
  });
}

function openAdminUserEditor(id) {
  const user = id ? _adminUsers.find(candidate => candidate.id === id) : null;
  if (id && !user) return;
  if (user && (user.id === 'break-glass' || (_adminPrincipal && user.id === _adminPrincipal.userID))) return;
  _editingAdminUser = user || null;
  _('adminUserID').value = user ? user.id : '';
  _('adminUserRevision').value = user ? String(user.revision || 0) : '0';
  _('adminUserProvider').value = user ? (user.provider || '') : '';
  _('adminUserUsername').value = user ? user.username : '';
  _('adminUserUsername').disabled = !!user;
  _('adminUserPassword').value = '';
  _('adminUserPassword').placeholder = user ? t('留空则不重置密码') : t('永久密码（至少 8 位）');
  _('adminUserPassword').disabled = !!(user && user.provider);
  _('adminUserPasswordField').hidden = !!(user && user.provider);
  _('adminUserDisabled').checked = !!(user && user.disabled);
  _('adminUserDisabled').disabled = !user;
  _('adminUserDisabledRow').hidden = !user;
  setAdminUserRoles(user ? user.roles : ['viewer'], !!(user && user.provider));
  _('adminUserEditorTitle').textContent = t(user ? '编辑管理员' : '添加本地管理员');
  _('adminUserError').textContent = '';
  _('adminUserEditor').style.display = '';
  (user ? _('adminUserDisabled') : _('adminUserUsername')).focus();
}

function closeAdminUserEditor() {
  _editingAdminUser = null;
  if (_('adminUserEditor')) _('adminUserEditor').style.display = 'none';
  if (_('adminUserError')) _('adminUserError').textContent = '';
  if (_('adminUserPassword')) _('adminUserPassword').value = '';
}

async function saveAdminUser(button) {
  const error = _('adminUserError'); error.textContent = '';
  const roles = adminUserSelectedRoles();
  const password = _('adminUserPassword').value;
  if (!roles.length) { error.textContent = t('至少选择一个角色'); return; }
  if (!_editingAdminUser && adminPasswordTooShort(password)) { error.textContent = t('永久密码至少 8 位'); return; }
  if (_editingAdminUser && password && adminPasswordTooShort(password)) { error.textContent = t('新密码至少 8 位'); return; }
  const original = button.innerHTML;
  button.disabled = true;
  button.innerHTML = '<span class="loading"></span> ' + t('保存中...');
  try {
    if (!_editingAdminUser) {
      const username = _('adminUserUsername').value.trim();
      if (!username) throw new Error(t('请输入管理员用户名'));
      await api('POST', '/auth/users', { username: username, password: password, roles: roles });
    } else {
      let revision = _editingAdminUser.revision;
      const disabled = _('adminUserDisabled').checked;
      const rolesChanged = JSON.stringify(roles.slice().sort()) !== JSON.stringify((_editingAdminUser.roles || []).slice().sort());
      if (rolesChanged || disabled !== !!_editingAdminUser.disabled) {
        const response = await api('POST', '/auth/users/update', { userID: _editingAdminUser.id, revision: revision, roles: roles, disabled: disabled });
        revision = response.data.user.revision;
      }
      if (password) await api('POST', '/auth/users/password', { userID: _editingAdminUser.id, revision: revision, password: password });
    }
    toast(t('管理员用户已保存'), 'success'); closeAdminUserEditor(); await loadAdminUsers();
  } catch (e) { error.textContent = e.message; }
  finally { button.disabled = false; button.innerHTML = original; }
}

async function deleteAdminUser(id, button) {
  const user = _adminUsers.find(candidate => candidate.id === id);
  if (!user || user.provider || user.id === 'break-glass') return;
  if (!(await showConfirm(t('确定删除此本地管理员？')))) return;
  button.disabled = true;
  try {
    await api('POST', '/auth/users/delete', { userID: user.id, revision: user.revision });
    if (_editingAdminUser && _editingAdminUser.id === id) closeAdminUserEditor();
    toast(t('管理员用户已删除'), 'success'); await loadAdminUsers();
  } catch (e) { toast(e.message, 'error'); }
  finally { button.disabled = false; }
}
