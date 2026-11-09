#!/usr/bin/env python3
"""Check rex-micro's outcomes in the real Rex app, independently of rex-micro.

  check_rex_micro.py press KEY [--double] [--keep]
      Press an Agent Key through the running daemon (`rex-micro press`), then
      read Rex itself: the key's target pane must be the focused block of its
      session's active window, and that session must be on screen. The pane
      shown before is restored unless --keep.
  check_rex_micro.py agents
      List agent panes on this Mac and every `rex hosts` host straight from
      Rex (program_status), and compare with the Agent Keys in
      `rex-micro status --json`.
  check_rex_micro.py judge-controls
      Run the judges on recorded fixtures: an accepted result must pass and
      planted faults must fail.

Exit 0 = PASS, 1 = FAIL, 2 = setup problem (nothing judged).
"""

import json
import subprocess
import sys
import time
from datetime import datetime, timezone


def run(args, timeout=20):
    p = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if p.returncode != 0:
        raise RuntimeError(f"{' '.join(args)}: {p.stderr.strip() or p.stdout.strip()}")
    return p.stdout


def rex(host, *args):
    return run(["rex", *(["-S", host] if host else []), *args])


def rexj(host, *args):
    return json.loads(rex(host, *args))


def status():
    return json.loads(run(["rex-micro", "status", "--json"]))


def app_label():
    """Label of this Mac's Rex app; it joins remote hosts under the same label."""
    for c in rexj("", "client", "ls", "--json")["clients"]:
        info = c.get("info") or {}
        if info.get("kind") == "app" and (c.get("principal") or {}).get("transport") == "unix":
            return info.get("label") or c["client_id"]
    raise RuntimeError("no Rex app is connected on this Mac")


def app_clients(host, label):
    ids = set()
    for c in rexj(host, "client", "ls", "--json")["clients"]:
        info = c.get("info") or {}
        if info.get("kind") == "app" and (info.get("label") == label or c["client_id"] == label):
            ids.add(c["client_id"])
    return ids


def focus_of(host, session):
    s = rexj(host, "session", "inspect", session, "--json")
    aw = s.get("active_window_id")
    for w in s.get("windows", []):
        if w.get("window_id") == aw:
            return aw, w.get("focused_block_id")
    return aw, None


def sized_at(host, session, block, clients):
    """When this Mac's app last sized block (None if never)."""
    out = json.loads(rex(host, "block", "call", "-s", session, "-b", block, "size"))
    times = [datetime.fromisoformat(d["updated_at"]) for d in out.get("desired", []) if d.get("client_id") in clients]
    return max(times) if times else None


def judge_press(target, before, after_focus, after_sized, pressed_at):
    """Verdict lines for one press. target/before: {host, session, window, block};
    after_focus: (active_window, focused_block) of target's session;
    after_sized: when the app last sized the target pane."""
    fails = []
    aw, fb = after_focus
    if aw != target["window"] or fb != target["block"]:
        fails.append(f"target session's focus is window {aw} block {fb}, want {target['window']} {target['block']}")
    same_tab = before and before.get("session") == target["session"] and before.get("host", "") == target.get("host", "") and before.get("window") == target["window"]
    if not same_tab and (after_sized is None or after_sized < pressed_at):
        fails.append(f"the app did not show the target session or tab (pane last sized {after_sized}, press at {pressed_at})")
    return fails


def show(host, session, window, block, label):
    rex(host, "focus", "-s", session, block)
    rex("", "-C", label, "do", "session.select", f"session_id={session}", f"window_id={window}")


def press(key, double, keep):
    st = status()
    if st.get("paused"):
        return setup("the daemon is paused (a `rex-micro test` runs)")
    slot = next((s for s in st["agent_keys"] if s["key"] == key.upper()), None)
    if not slot or not slot.get("block"):
        return setup(f"{key} follows no agent now; pick a key that `rex-micro status` shows with a pane")
    target = {"host": slot.get("host", ""), "session": slot["session_id"], "window": slot["window"], "block": slot["block"]}
    before = st.get("shown_pane")
    if before and before["block"] == target["block"]:
        return setup(f"{key}'s pane is already on screen; pick another key or show another pane first")
    label = app_label()
    clients = app_clients(target["host"], label)
    kind = "same session" if before and before["session"] == target["session"] else "another session"
    print(f"{key}: {slot.get('app')} on {target['host'] or 'this Mac'}, session {slot.get('session')} ({kind}), pane {target['block']}")
    print(f"shown before: {json.dumps(before)}")
    pressed_at = datetime.now(timezone.utc)
    out = subprocess.run(["rex-micro", "press", key, *(["--double"] if double else [])], capture_output=True, text=True, timeout=40)
    print(out.stdout.strip())
    if out.returncode != 0:
        print(out.stderr.strip())
        if before and not keep:
            restore(before, label)
        return fail([f"rex-micro press exited {out.returncode}"])
    settle_app_resize = 0.5
    try:
        time.sleep(settle_app_resize)
        after_focus = focus_of(target["host"], target["session"])
        after_sized = sized_at(target["host"], target["session"], target["block"], clients)
        print(f"after: focus {after_focus}, sized {after_sized}, rex-micro sees {json.dumps(status().get('shown_pane'))}")
        fails = judge_press(target, before, after_focus, after_sized, pressed_at)
    finally:
        if before and not keep:
            restore(before, label)
    return fail(fails) if fails else ok()


def restore(before, label):
    try:
        show(before.get("host", ""), before["session"], before["window"], before["block"], label)
        print(f"restored {before['session_label']} / {before['block']}")
    except (RuntimeError, subprocess.TimeoutExpired) as e:
        print(f"RESTORE FAILED, put the view back by hand: {e}")


def rex_agents():
    """Every agent pane on this Mac and each host, read from Rex itself."""
    found, unreachable = [], []
    hosts = [""] + [line.split()[0] for line in rex("", "hosts").splitlines()[1:] if line.strip()]
    for h in hosts:
        try:
            sessions = rexj(h, "ls", "--json")["sessions"]
        except Exception as e:
            unreachable.append(f"{h}: {e}")
            continue
        for s in sessions:
            for b in rexj(h, "block", "ls", "-s", s["session_id"], "--json")["blocks"]:
                if not b.get("flavor", "").startswith("com.superlogical.terminal"):
                    continue
                try:
                    st = json.loads(rex(h, "block", "call", "-s", s["session_id"], "-b", b["block_id"], "program_status"))
                except RuntimeError:
                    continue
                if any(r.get("effective_app") or r.get("app") for r in st.get("records") or []):
                    found.append({"host": h, "session": s.get("label"), "block": b["block_id"]})
    return found, unreachable


def judge_agents(keys, found, mode):
    fails = []
    by_block = {f["block"]: f for f in found}
    shown = [k for k in keys if k.get("block")]
    for k in shown:
        f = by_block.get(k["block"])
        if f is None:
            fails.append(f"{k['key']} follows {k['block']}, which Rex does not list as an agent")
        elif f["host"] != k.get("host", ""):
            fails.append(f"{k['key']} says host {k.get('host') or 'this Mac'}, Rex has it on {f['host'] or 'this Mac'}")
    targets = [(k.get("host", ""), k["block"]) for k in shown]
    dupes = sorted({t for t in targets if targets.count(t) > 1})
    if dupes:
        fails.append(f"several keys follow the same pane: {dupes}")
    if mode == "auto" and len(set(targets)) < min(len(keys), len(found)):
        missing = [f for f in found if (f["host"], f["block"]) not in set(targets)]
        fails.append(f"{len(found)} agents in Rex but only {len(set(targets))} distinct panes on keys; missing {missing}")
    return fails


def agents():
    st = status()
    found, unreachable = rex_agents()
    for u in unreachable:
        print("unreachable (skipped):", u)
    hosts = sorted({f["host"] or "this Mac" for f in found})
    print(f"Rex lists {len(found)} agent panes on {', '.join(hosts) or 'no host'}")
    for f in found:
        print(f"  {f['host'] or 'this Mac'}\t{f['session']}\t{f['block']}")
    for k in st["agent_keys"]:
        print(f"  {k['key']}\t{k.get('host') or ('this Mac' if k.get('block') else '-')}\t{k.get('session', '-')}\t{k.get('block', '-')}")
    mode = "auto"
    try:
        import tomllib
        with open(st["config"], "rb") as fh:
            mode = tomllib.load(fh).get("agents", {}).get("mode", "auto")
    except FileNotFoundError:
        pass
    fails = judge_agents(st["agent_keys"], found, mode)
    return fail(fails) if fails else ok()


def judge_controls():
    t = {"host": "hostA", "session": "s2", "window": "w2", "block": "b2"}
    before = {"host": "", "session": "s1", "window": "w1", "block": "b1"}
    at = datetime(2026, 11, 9, 12, 0, tzinfo=timezone.utc)
    later, earlier = datetime(2026, 11, 9, 12, 0, 1, tzinfo=timezone.utc), datetime(2026, 11, 9, 11, 0, tzinfo=timezone.utc)
    cases = [
        ("accepted: other session focused and shown", judge_press(t, before, ("w2", "b2"), later, at), False),
        ("accepted: same tab, focus moved", judge_press(t, dict(t, block="b9"), ("w2", "b2"), earlier, at), False),
        ("fault: pane focused but its session not shown", judge_press(t, before, ("w2", "b2"), earlier, at), True),
        ("fault: session shown, other pane focused", judge_press(t, before, ("w2", "b3"), later, at), True),
        ("fault: same session id on another host", judge_press(t, dict(t, host="", block="b9"), ("w2", "b2"), earlier, at), True),
    ]
    found = [{"host": "", "block": "a"}, {"host": "hostB", "block": "b"}]
    keys = [{"key": "AG00", "block": "a"}, {"key": "AG01", "block": "b", "host": "hostB"}] + [{"key": f"AG0{i}"} for i in range(2, 6)]
    dropped_other_host = [{"key": "AG00", "block": "a"}] + [{"key": f"AG0{i}"} for i in range(1, 6)]
    wrong = [{"key": "AG00", "block": "a"}, {"key": "AG01", "block": "b"}] + [{"key": f"AG0{i}"} for i in range(2, 6)]
    duplicated = [{"key": "AG00", "block": "a"}, {"key": "AG01", "block": "a"}] + [{"key": f"AG0{i}"} for i in range(2, 6)]
    cases += [
        ("fault: other tab of the shown session focused but not shown", judge_press(t, dict(t, window="w1", block="b9"), ("w2", "b2"), earlier, at), True),
        ("fault: two keys on one agent, the other host's agent keyless", judge_agents(duplicated, found, "auto"), True),
        ("accepted: keys cover both hosts", judge_agents(keys, found, "auto"), False),
        ("fault: another host's agent dropped", judge_agents(dropped_other_host, found, "auto"), True),
        ("fault: key names the wrong host", judge_agents(wrong, found, "auto"), True),
    ]
    bad = [name for name, fails, want in cases if bool(fails) != want]
    for name, fails, want in cases:
        print(f"{'ok ' if bool(fails) == want else 'BAD'} {name}{': ' + '; '.join(fails) if fails else ''}")
    return fail([f"judge misjudged {b}" for b in bad]) if bad else ok()


def ok():
    print("PASS")
    return 0


def fail(lines):
    for line in lines:
        print("FAIL:", line)
    return 1


def setup(msg):
    print("SETUP:", msg)
    return 2


def main(argv):
    if not argv:
        print(__doc__)
        return 2
    cmd, rest = argv[0], argv[1:]
    try:
        if cmd == "press" and rest:
            return press(rest[0], "--double" in rest, "--keep" in rest)
        if cmd == "agents":
            return agents()
        if cmd == "judge-controls":
            return judge_controls()
    except (RuntimeError, subprocess.TimeoutExpired, KeyError) as e:
        return setup(str(e))
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
