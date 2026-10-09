#!/usr/bin/env python3
"""Run agentnetd with a real pseudo-terminal on stdin/stderr (DORYLINAE_APPROVAL=terminal,
no DORYLINAE_DEBUG), record that terminal to a transcript file, and act as the human at the
daemon's terminal: when an approval line appears, wait a short moment and type
"<tag> <code>" back, exactly as a person watching the terminal would.
Usage: daemon_pty.py <agentnetd> <transcript> <stdout-file> [approve-delay-seconds]
Env (DORYLINAE_HOME, DORYLINAE_RELAY_URL, ...) is passed through; APPROVAL forced to terminal.
Set AUTO_APPROVE=0 to not answer approvals (lines can then be typed via the <transcript>.in FIFO).
"""
import os, pty, re, signal, subprocess, sys, threading, time, select

exe, transcript, outfile = sys.argv[1], sys.argv[2], sys.argv[3]
delay = float(sys.argv[4]) if len(sys.argv) > 4 else 2.0
auto = os.environ.get("AUTO_APPROVE", "1") != "0"
home = os.environ["DORYLINAE_HOME"]
logfile = os.path.join(home, "agentnetd.log")

master, slave = pty.openpty()
env = dict(os.environ, DORYLINAE_APPROVAL="terminal")
env.pop("DORYLINAE_DEBUG", None)
out = open(outfile, "ab")
p = subprocess.Popen([exe, "run", "--log-file", logfile], stdin=slave, stdout=out, stderr=slave,
                     env=env, close_fds=True, start_new_session=True)
os.close(slave)
rx = re.compile(rb"AgentNet approval (a-[0-9a-f]+): .*?Code ([0-9]+)[.]")
tf = open(transcript, "ab")
lock = threading.Lock()

def type_line(b):
    with lock:
        os.write(master, b + b"\n")

def answer(tag, code):
    time.sleep(delay)
    type_line(tag + b" " + code)

def reader():
    buf = b""
    while True:
        try:
            r, _, _ = select.select([master], [], [], 0.5)
            if not r:
                if p.poll() is not None:
                    return
                continue
            data = os.read(master, 4096)
        except OSError:
            return
        if not data:
            return
        tf.write(data); tf.flush()
        buf += data
        while b"\n" in buf:
            line, buf = buf.split(b"\n", 1)
            m = rx.search(line)
            if m and auto:
                threading.Thread(target=answer, args=(m.group(1), m.group(2)), daemon=True).start()

def fifo_reader():
    fifo = transcript + ".in"
    try:
        os.mkfifo(fifo, 0o600)
    except FileExistsError:
        pass
    while p.poll() is None:
        with open(fifo, "rb") as f:
            for line in f:
                type_line(line.rstrip(b"\n"))

threading.Thread(target=reader, daemon=True).start()
threading.Thread(target=fifo_reader, daemon=True).start()

def term(*_):
    try:
        p.terminate()
    except Exception:
        pass
signal.signal(signal.SIGTERM, term)
signal.signal(signal.SIGINT, term)
rc = p.wait()
time.sleep(0.3)
tf.write(("\n[agentnetd exited %d]\n" % rc).encode()); tf.flush()
sys.exit(rc)
