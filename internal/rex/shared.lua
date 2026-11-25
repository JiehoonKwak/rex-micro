-- rex-micro: helpers that snapshot.lua and show.lua share; rex-micro
-- prepends this file to both.

-- instant turns an RFC 3339 time into seconds since 1970, so times with
-- different offsets or fractions compare correctly; 0 when unreadable.
local function instant(s)
  local y, mo, d, h, mi, sec, frac, tz = (s or ""):match("^(%d+)-(%d+)-(%d+)[Tt ](%d+):(%d+):(%d+)(%.?%d*)(.*)$")
  if not y then return 0 end
  y, mo, d = tonumber(y), tonumber(mo), tonumber(d)
  if mo <= 2 then y = y - 1 end
  local era = math.floor(y / 400)
  local yoe = y - era * 400
  local doy = math.floor((153 * ((mo + 9) % 12) + 2) / 5) + d - 1
  local days = era * 146097 + yoe * 365 + math.floor(yoe / 4) - math.floor(yoe / 100) + doy - 719468
  local t = days * 86400 + tonumber(h) * 3600 + tonumber(mi) * 60 + tonumber(sec) + (tonumber("0" .. frac) or 0)
  local sign, oh, om = tz:match("^([+-])(%d%d):?(%d%d)$")
  if sign then
    local off = tonumber(oh) * 3600 + tonumber(om) * 60
    t = sign == "+" and t - off or t + off
  end
  return t
end

-- app_client finds the Rex app client on server srv (rex, or rex.server(host))
-- that pref names (a label or ID), else the app on that server's local
-- socket. On another host's server pref is this Mac's app's label, under
-- which the app joins every host. It returns the client's ID and label, or
-- nil and why. The newest match wins: an old connection may linger after Rex
-- restarts.
local function app_client(srv, pref)
  local list, err = srv.call("client.list", {})
  if err then return nil, nil, err end
  local found, label, newest = nil, nil, -1
  for _, c in ipairs((list or {}).clients or {}) do
    local info = c.info or {}
    local wanted
    if pref ~= "" then
      wanted = c.client_id == pref or info.label == pref
    else
      wanted = (c.principal or {}).transport == "unix"
    end
    local at = instant(c.connected_at)
    if info.kind == "app" and wanted and at > newest then
      found, label, newest = c.client_id, info.label, at
    end
  end
  if not found then return nil, nil, "no Rex app (" .. pref .. ") is connected; open Rex" end
  return found, label
end
