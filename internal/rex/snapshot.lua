-- rex-micro: list every pane in every session with its agent state. Each
-- session's focused terminal also reports its foreground program and when an
-- app last sized it, which tells which session the Rex app is showing.
-- rex-micro prepends `local app_pref = "<label or ID>"` (rex.app, "" = auto).
-- Only that app's sizing counts, since another client (a phone, a second Mac)
-- sizes what it shows. The client is looked up on every run, so a Rex
-- restart (a new client ID) is picked up at once.
local terminal = "com.superlogical.terminal"
local app_client, newest_app = "", ""
for _, c in ipairs((rex.call("client.list", {}) or {}).clients or {}) do
  local info = c.info or {}
  local wanted
  if app_pref ~= "" then
    wanted = c.client_id == app_pref or info.label == app_pref
  else
    wanted = (c.principal or {}).transport == "unix"
  end
  -- The newest match wins: an old connection may linger after Rex restarts.
  if info.kind == "app" and wanted and (c.connected_at or "") > newest_app then
    app_client, newest_app = c.client_id, c.connected_at or ""
  end
end
-- "/bin/zsh" -> "zsh"; a login shell's argv0 "-zsh" -> "zsh"
local function base(path) return ((path or ""):match("([^/]+)$") or ""):gsub("^%-", "") end
local panes = {}
local list = rex.call("session.list", {}) or {}
for index, session in ipairs(list.sessions or {}) do
  local sid = session.session_id
  rex.call("session.attach", { session_id = sid })
  local view = rex.call("session.view", { session_id = sid }) or {}
  for _, window in ipairs(view.windows or {}) do
    local active = window.window_id == view.active_window_id
    for _, layer in ipairs(window.layers or {}) do
      for _, block in ipairs(layer.blocks or {}) do
        local pane = {
          session = sid, session_label = session.label or "", session_index = index,
          window = window.window_id, block = block.block_id, label = block.label or "",
          focused = active and window.focused_block_id == block.block_id,
        }
        if block.creator_name == terminal then
          local ok, status = pcall(rex.block.call, terminal, "program_status",
            { session_id = sid, block_id = block.block_id })
          local record = ok and status and (status.records or {})[1] or nil
          if record and record.app and record.app ~= "" then
            pane.app = record.effective_app or record.app
            pane.state = record.state or "idle"
            if record.updated_at and record.updated_at ~= "" then pane.since = record.updated_at end
          end
          if pane.focused then
            local args = { session_id = sid, block_id = block.block_id }
            local okp, proc = pcall(rex.block.call, terminal, "process", args)
            local fg = okp and proc and proc.foreground or nil
            if fg then
              pane.program = base(fg.argv0) ~= "" and base(fg.argv0) or base(fg.invoked_path) ~= "" and base(fg.invoked_path) or fg.name or ""
            end
            local oks, size = pcall(rex.block.call, terminal, "size", args)
            for _, d in ipairs(oks and size and size.desired or {}) do -- newest first
              if (app_client == "" or d.client_id == app_client) and d.updated_at then
                pane.shown_at = d.updated_at
                break
              end
            end
          end
        end
        panes[#panes + 1] = pane
      end
    end
  end
end
return panes
