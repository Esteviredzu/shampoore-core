#!/usr/bin/env python3
import itertools, json, os, subprocess, time, csv, signal, socket

CLIENT_BIN = "./bin/client"
SERVER_BIN = "./bin/server"
CLIENT_CFG = "config_yandextelemost_client.json"
SERVER_CFG = "config_yandextelemost_server.json"

TEST_URL   = "https://speed.cloudflare.com/__down?bytes=100000"
PRIME_URL  = "https://speed.cloudflare.com/__down?bytes=20000"
PROXY      = "socks5h://127.0.0.1:1080"
SOCKS_ADDR = ("127.0.0.1", 1080)

BASELINE = {
    "w": 640, "h": 360, "cols": 40, "rows": 20,
    "fps": 30, "br": "4M",
    "win": 256, "int": 5, "res": 5,
}

RESOLUTIONS = [
    (640,  360,  40, 20),
    (854,  480,  53, 30),
    (1280, 720,  80, 45),
]
BITRATES = ["4M"]
FPS_VALS = [30]
KCP_WIN = [256]
KCP_INT = [5]
KCP_RES = [5]
MTU_MAX = 1200

SERVER_READY_TIMEOUT = 40
SOCKS_READY_TIMEOUT  = 40
WARMUP_SETTLE_MAX    = 45    # hard cap: 20-30s + margin
WARMUP_IDLE          = 3
CURL_TIMEOUT         = 120
PORT_FREE_WAIT       = 15

os.makedirs("logs", exist_ok=True)

def load_cfg(path):
    with open(path) as f:
        return json.load(f)

CLIENT_BASE = load_cfg(CLIENT_CFG)
SERVER_BASE = load_cfg(SERVER_CFG)
CLIENT_BASE.setdefault("advanced", {})
SERVER_BASE.setdefault("advanced", {})

def valid(w, h, c, r):
    if c * 16 > w or r * 16 > h:
        return False
    cells = c * r
    if cells % 2 != 0:
        return False
    if cells // 2 - 10 < 64:
        return False
    return True

def make_advanced(w, h, c, r, fps, br, win, inter, res):
    cells = c * r
    payload = cells // 2
    max_seg = payload - 10
    mtu = min(MTU_MAX, max_seg)
    return {
        "mux_flush_ms": 10,
        "mux_flush_threshold": 4000,
        "kcp_conv": 0,
        "kcp_mtu": mtu,
        "kcp_window": win,
        "kcp_nodelay": 1,
        "kcp_interval": inter,
        "kcp_resend": res,
        "kcp_nc": True,
        "squares_w": w,
        "squares_h": h,
        "squares_cols": c,
        "squares_rows": r,
        "squares_fps": fps,
        "squares_bitrate": br,
        "squares_copies": 1,
    }

def write_cfg(path, base, adv):
    cfg = dict(base)
    cfg["advanced"] = adv
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)

def port_free(addr):
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        s.bind(addr); return True
    except OSError:
        return False
    finally:
        s.close()

def wait_port_free(addr, timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if port_free(addr): return True
        time.sleep(0.3)
    return False

def tcp_reachable(addr, timeout=1.0):
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(timeout); s.connect(addr); s.close(); return True
    except OSError:
        return False

def log_has(path, needle):
    if not os.path.exists(path): return False
    try:
        with open(path, "r", errors="ignore") as f:
            return needle in f.read()
    except Exception:
        return False

def wait_for(cond, timeout, interval=0.5):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if cond(): return True
        time.sleep(interval)
    return False

def do_curl(url, timeout, expected_bytes=None):
    """returns (speed_Bps, received_bytes, elapsed_s, err)"""
    try:
        r = subprocess.run(
            ["curl", "-sS", "-o", "/dev/null",
             "-w", "%{speed_download} %{size_download} %{time_total}",
             "--max-time", str(timeout), url],
            env={**os.environ, "ALL_PROXY": PROXY},
            timeout=timeout + 5, capture_output=True, text=True,
        )
        parts = (r.stdout or "").strip().split()
        if len(parts) < 3:
            return 0.0, 0, 0.0, (r.stderr or "").strip() or f"curl rc={r.returncode}"
        try:
            speed = float(parts[0])
            received = int(parts[1])
            dur = float(parts[2])
        except ValueError:
            return 0.0, 0, 0.0, f"bad curl stdout: {r.stdout!r}"
        if r.returncode != 0:
            return speed, received, dur, \
                (r.stderr or "").strip() or f"curl rc={r.returncode}"
        if expected_bytes and received < expected_bytes * 0.95:
            return speed, received, dur, f"incomplete {received}/{expected_bytes}"
        return speed, received, dur, ""
    except subprocess.TimeoutExpired:
        return 0.0, 0, 0.0, "curl process timeout"
    except Exception as e:
        return 0.0, 0, 0.0, str(e)

def warmup():
    """Drive real traffic through tunnel until speed stabilizes.
    Idle frames don't feed KCP; only real data grows cwnd.
    Exits on 3 consecutive stable windows (±15%), or at WARMUP_SETTLE_MAX."""
    print("warmup:", end="", flush=True)
    deadline = time.time() + WARMUP_SETTLE_MAX
    speeds = []
    timeouts_in_row = 0
    any_ok = False

    while time.time() < deadline:
        try:
            r = subprocess.run(
                ["curl", "-sS", "-o", "/dev/null",
                 "-w", "%{speed_download} %{size_download}",
                 "--max-time", "20", PRIME_URL],
                env={**os.environ, "ALL_PROXY": PROXY},
                timeout=25, capture_output=True, text=True,
            )
            parts = (r.stdout or "").strip().split()
            got = 0
            sp = 0.0
            if len(parts) >= 2:
                try:
                    sp = float(parts[0]); got = int(parts[1])
                except ValueError:
                    pass
            if got > 0:
                any_ok = True
                timeouts_in_row = 0
                speeds.append(sp)
                print(f" {sp/1024:.1f}", end="", flush=True)
                if len(speeds) >= 3:
                    recent = speeds[-3:]
                    mx, mn = max(recent), min(recent)
                    if mn > 0 and (mx - mn) / mx < 0.15:
                        break
            else:
                timeouts_in_row += 1
                print(" !", end="", flush=True)
                if timeouts_in_row >= 3:
                    break
        except subprocess.TimeoutExpired:
            timeouts_in_row += 1
            print(" T", end="", flush=True)
            if timeouts_in_row >= 3:
                break

    time.sleep(WARMUP_IDLE)
    print(" → measure", flush=True)
    return any_ok

def run_one(adv, tag):
    cli_log = f"logs/cli_{tag}.log"
    srv_log = f"logs/srv_{tag}.log"

    write_cfg(CLIENT_CFG, CLIENT_BASE, adv)
    write_cfg(SERVER_CFG, SERVER_BASE, adv)

    if not wait_port_free(SOCKS_ADDR, PORT_FREE_WAIT):
        return 0.0, 0.0, 0, "port 1080 busy", cli_log, srv_log

    with open(srv_log, "w") as sf, open(cli_log, "w") as cf:
        srv = subprocess.Popen([SERVER_BIN, "-c", SERVER_CFG],
                               stdout=sf, stderr=subprocess.STDOUT)
        try:
            if not wait_for(lambda: log_has(srv_log, "server: ready"),
                            SERVER_READY_TIMEOUT):
                return 0.0, 0.0, 0, "server never ready", cli_log, srv_log
            cli = subprocess.Popen([CLIENT_BIN, "-c", CLIENT_CFG],
                                   stdout=cf, stderr=subprocess.STDOUT)
            try:
                if not wait_for(
                    lambda: log_has(cli_log, "socks5: listening on") and
                            tcp_reachable(SOCKS_ADDR),
                    SOCKS_READY_TIMEOUT,
                ):
                    return 0.0, 0.0, 0, "socks5 never came up", cli_log, srv_log
                if not warmup():
                    return 0.0, 0.0, 0, "warmup failed (no data)", cli_log, srv_log
                speed, received, dur, err = do_curl(TEST_URL, CURL_TIMEOUT,
                                                    expected_bytes=100000)
                return speed, dur, received, err, cli_log, srv_log
            finally:
                try: cli.send_signal(signal.SIGINT)
                except ProcessLookupError: pass
                try: cli.wait(4)
                except subprocess.TimeoutExpired:
                    cli.kill(); cli.wait(2)
        finally:
            try: srv.send_signal(signal.SIGINT)
            except ProcessLookupError: pass
            try: srv.wait(4)
            except subprocess.TimeoutExpired:
                srv.kill(); srv.wait(2)

def restore():
    write_cfg(CLIENT_CFG, CLIENT_BASE, CLIENT_BASE["advanced"])
    write_cfg(SERVER_CFG, SERVER_BASE, SERVER_BASE["advanced"])

def tail(path, n=25):
    try:
        with open(path, "rb") as f:
            f.seek(0, 2); size = f.tell()
            f.seek(max(0, size - n * 200))
            return f.read().decode("utf-8", errors="replace")
    except Exception as e:
        return f"<{e}>"

def main():
    combos = []
    for (w, h, c, r), br, fps in itertools.product(RESOLUTIONS, BITRATES, FPS_VALS):
        if valid(w, h, c, r):
            combos.append((w, h, c, r, fps, br,
                           KCP_WIN[0], KCP_INT[0], KCP_RES[0]))
    base = (BASELINE["w"], BASELINE["h"], BASELINE["cols"], BASELINE["rows"],
            BASELINE["fps"], BASELINE["br"], BASELINE["win"],
            BASELINE["int"], BASELINE["res"])

    # BASELINE в начале и в конце — оценка воспроизводимости
    ordered = [base] + [c for c in combos if c != base] + [base]

    print(f"Всего комбинаций: {len(ordered)}", flush=True)
    print(f"TEST_URL={TEST_URL}  CURL_TIMEOUT={CURL_TIMEOUT}s  "
          f"WARMUP_SETTLE_MAX={WARMUP_SETTLE_MAX}s", flush=True)

    import hashlib
    for b in (CLIENT_BIN, SERVER_BIN):
        try:
            h = hashlib.md5(open(b, "rb").read()).hexdigest()[:8]
            print(f"{b}: md5={h} mtime={time.ctime(os.path.getmtime(b))}",
                  flush=True)
        except OSError as e:
            print(f"{b}: MISSING ({e})", flush=True); return

    with open("bench_res.csv", "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["w","h","cols","rows","fps","bitrate",
                         "kcp_win","kcp_int","kcp_res",
                         "speed_Bps","received_B","elapsed_s","err"])
        try:
            for i, params in enumerate(ordered, 1):
                w, h, c, r, fps, br, win, inter, res = params
                is_base = params == base
                tag = f"{w}x{h}_{c}x{r}_f{fps}_{br}_w{win}_i{inter}_r{res}"
                if is_base:
                    tag += f"_BASE{i}"
                adv = make_advanced(*params)
                prefix = f"BASE{i}" if is_base else f"{i:4d}"
                print(f"\n[{prefix}] {tag}", flush=True)

                speed, elapsed, received, err, cl, sl = run_one(adv, tag)
                print(f"  → {speed/1024:.1f} KiB/s "
                      f"({elapsed:.1f}s, {received} B) {err}", flush=True)
                writer.writerow([*params, speed, received, elapsed, err])
                f.flush()

                if err:
                    print(f"  logs: {cl}  {sl}", flush=True)
                    print(f"  --- client tail ---\n{tail(cl)}", flush=True)
                    print(f"  --- server tail ---\n{tail(sl)}", flush=True)
        except KeyboardInterrupt:
            print("\nОстановлено пользователем.")
        finally:
            restore()

if __name__ == "__main__":
    main()