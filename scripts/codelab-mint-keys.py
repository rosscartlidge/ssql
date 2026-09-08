# Drives the codelab's "Try it" keys through a real pty inside the mint
# container (used by codelab-mint.sh). Prints what each key produced and
# exits non-zero if any expectation is missing.
import os, pty, sys, time, select
def run(keys, wait=3.0):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ["TERM"] = "xterm"
        os.environ["PATH"] = os.environ["PATH"] + ":" + os.path.expanduser("~/go/bin")
        os.execvp("bash", ["bash", "--norc", "-i"])
    def rd(t):
        out = b""; end = time.time() + t
        while time.time() < end:
            r, _, _ = select.select([fd], [], [], 0.2)
            if r:
                try: out += os.read(fd, 65536)
                except OSError: break
        return out
    rd(1.0)
    os.write(fd, b'eval "$(ssql -shell-init)"\n'); rd(2.0)
    os.write(fd, b'cd ~/ssql-codelab\n'); rd(0.5)
    os.write(fd, keys); out = rd(wait)
    os.write(fd, b'\x03exit\n'); rd(0.5)
    try: os.close(fd)
    except OSError: pass
    return out.decode("utf-8", "replace").replace("\r", "")
cases = [
    ("Tab completes the command", b"ssql fr\t", "ssql from"),
    ("Tab in a field slot inserts the Use-Ctrl-O reminder", b"ssql from employees.csv | ssql where -if \t", "Use-Ctrl-O"),
    ("Ctrl-O lists field names", b"ssql from employees.csv | ssql where -if \x0f", "salary"),
    ("Ctrl-O lists values", b"ssql from employees.csv | ssql where -if dept eq \x0f", "Engineering"),
    ("Alt-h explains the flag", b"ssql from employees.csv | ssql group-by dept -sum salary\x1bh", "Sum field values"),
]
bad = 0
for name, keys, want in cases:
    out = run(keys)
    ok = want in out
    bad += 0 if ok else 1
    print(("ok   " if ok else "FAIL ") + name)
    if not ok:
        print(out[-600:])
sys.exit(1 if bad else 0)
