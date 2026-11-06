-- rex-micro: list every pane in every session with its agent state. Each
-- session's focused terminal also reports its foreground program and when an
-- app last sized it, which tells which session the Rex app is showing.
-- rex-micro prepends `local app_pref = "<label or ID>"` (rex.app, "" = auto)
-- and `local watch = <seconds>` (0 = print one snapshot and return).
-- Only that app's sizing counts, since another client (a phone, a second Mac)
-- sizes what it shows. The client is looked up on every snapshot, so a Rex
-- restart (a new client ID) is picked up at once.
local terminal = "com.superlogical.terminal"
-- Most urgent first, the same order the pad's agent keys use.
local urgency = { blocked = 0, error = 0, done = 1, working = 2, idle = 3 }

local function app_client()
  local found, newest = "", ""
  for _, c in ipairs((rex.call("client.list", {}) or {}).clients or {}) do
    local info = c.info or {}
    local wanted
    if app_pref ~= "" then
      wanted = c.client_id == app_pref or info.label == app_pref
    else
      wanted = (c.principal or {}).transport == "unix"
    end
    -- The newest match wins: an old connection may linger after Rex restarts.
    if info.kind == "app" and wanted and (c.connected_at or "") > newest then
      found, newest = c.client_id, c.connected_at or ""
    end
  end
  return found
end

-- "/bin/zsh" -> "zsh"; a login shell's argv0 "-zsh" -> "zsh"
local function base(path) return ((path or ""):match("([^/]+)$") or ""):gsub("^%-", "") end

-- A terminal holds one status record per id: the program's own (no id) and
-- one per child, such as each Claude subagent. Children without `app` take it
-- from the root. The pane shows the most urgent record.
local function agent_state(pane, records)
  local app, best = "", nil
  for _, r in ipairs(records or {}) do
    if (r.id or "") == "" and (r.effective_app or r.app or "") ~= "" then app = r.effective_app or r.app end
  end
  for _, r in ipairs(records or {}) do
    local rapp = r.effective_app or r.app or app
    if rapp ~= "" and (best == nil or (urgency[r.state or "idle"] or 3) < (urgency[best.state or "idle"] or 3)) then
      best = r
      if app == "" then app = rapp end
    end
  end
  if best then
    pane.app, pane.state = app, best.state or "idle"
    if best.updated_at and best.updated_at ~= "" then pane.since = best.updated_at end
  end
end

local shown_by = "" -- the app client of the last snapshot
local function snapshot(attach)
  local app = app_client()
  shown_by = app
  local panes = {}
  local list = rex.call("session.list", {}) or {}
  for index, session in ipairs(list.sessions or {}) do
    local sid = session.session_id
    -- A one-shot script must attach to read a session; the watcher hears
    -- every session already, and attaching each time would announce a
    -- client to every listener.
    if attach then rex.call("session.attach", { session_id = sid }) end
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
            local args = { session_id = sid, block_id = block.block_id }
            local ok, status = pcall(rex.block.call, terminal, "program_status", args)
            if ok and status then agent_state(pane, status.records) end
            if pane.focused then
              local okp, proc = pcall(rex.block.call, terminal, "process", args)
              local fg = okp and proc and proc.foreground or nil
              if fg then
                pane.program = base(fg.argv0) ~= "" and base(fg.argv0) or base(fg.invoked_path) ~= "" and base(fg.invoked_path) or fg.name or ""
              end
              local oks, size = pcall(rex.block.call, terminal, "size", args)
              for _, d in ipairs(oks and size and size.desired or {}) do -- newest first
                if (app == "" or d.client_id == app) and d.updated_at then
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
end

if watch == 0 then return snapshot(true) end

-- Watch: print a snapshot as one JSON line now, after each burst of changes,
-- and every `watch` seconds in case an event was missed.
local last
local function emit()
  local line = rex.json.encode(snapshot(false))
  if line and line ~= last then
    last = line
    io.stdout:write(line, "\n")
    io.stdout:flush()
  end
end
local pending
local function soon()
  if pending then pending:cancel() end
  pending = rex.after(0.1, emit)
end
-- Not title_changed: spinners rename a terminal several times a second.
for _, name in ipairs({
  "session_view_changed", "active_window_changed", "block_created", "block_closed",
  "session_created", "session_destroyed", "session_moved", "session_label_changed",
  "terminal.program_status_changed", "terminal.program_status_removed",
  "terminal.process_changed", "terminal.size_changed",
}) do
  rex.on(name, soon)
end
-- Every rex command joins and leaves; only an app arriving or the one in use
-- leaving changes whose sizing counts.
rex.on("client_joined", function(_, ev)
  if ((ev.client or {}).info or {}).kind == "app" then soon() end
end)
rex.on("client_left", function(_, ev)
  if ev.client_id == shown_by then soon() end
end)
rex.after(0, emit)
rex.every(watch, emit)
