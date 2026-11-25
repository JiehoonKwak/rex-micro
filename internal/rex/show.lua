-- rex-micro: bring a pane into view in the Rex app, in one rex process on
-- this Mac. rex-micro prepends shared.lua and passes --args:
--   app      [rex] app ("" = the app on this Mac's local socket)
--   host     the pane's Rex host label, "" for this Mac
--   session, window, block   the pane
-- The app looks up the session it selects only on the server that relays
-- the request, so a host's pane is shown through that host's server, as this
-- Mac's app's client there (found by its label). Focus first: the app does
-- not follow a focus change in a session it already shows, so focusing after
-- the select left the old pane on screen. rex.call returns nil and a message
-- on failure; it does not raise.
local a = rex.args
local srv, pref = rex, a.app
if a.host ~= "" then
  local _, label, err = app_client(rex, a.app)
  if err then error(err) end
  if (label or "") == "" then error("this Mac's Rex app has no label to find it on " .. a.host .. " by") end
  srv, pref = rex.server(a.host), label
end
local app, _, err = app_client(srv, pref)
if err then error(err) end
-- A script must attach to a session to change it.
_, err = srv.call("session.attach", { session_id = a.session })
if err then error(err) end
_, err = srv.call("session.focus_block", { session_id = a.session, block_id = a.block })
if err then error(err) end
_, err = srv.call("client.perform", {
  client_id = app, action = "session.select",
  args = { session_id = a.session, window_id = a.window },
})
if err then error(err) end
