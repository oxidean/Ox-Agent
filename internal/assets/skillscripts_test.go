package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pythonBinary locates a Python 3 interpreter, preferring python3 (POSIX
// convention) and falling back to python (Windows runner convention).
func pythonBinary(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no python interpreter available")
	return ""
}

// materializeSkillScripts expands the embedded asset tree into a temp dir and
// returns the scripts directory of the named skill.
func materializeSkillScripts(t *testing.T, skill string) string {
	t.Helper()
	base := t.TempDir()
	if _, err := Materialize(base); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	return filepath.Join(base, "skills", skill, "scripts")
}

// TestVendoredTextHygieneScripts exercises the vendored clean-user-facing-text
// fixes end-to-end against the embedded copies.
func TestVendoredTextHygieneScripts(t *testing.T) {
	py := pythonBinary(t)
	scripts := materializeSkillScripts(t, "clean-user-facing-text")

	driver := `
import os, stat, struct, subprocess, sys, tempfile
from pathlib import Path
sys.path.insert(0, os.environ["SCRIPTS_DIR"])
import common
from text_unicode import clean_text, inspect_text

failures = []
def check(name, cond, detail=""):
    if not cond:
        failures.append(f"{name}: {detail}")

# -- binary guard: text prefixes pass, real containers still refused --
for prefix in (b"BMW sales\n", b"BMI index\n", b"BM25 ranking\n", b"RIFF containers\n", b"OTTO specs\n"):
    check(f"text {prefix!r}", common.looks_binary(prefix * 20) is None)

bmp = b"BM" + struct.pack("<I", 78) + b"\x00" * 4 + struct.pack("<I", 54) + struct.pack("<I", 40) + b"\x00" * 60
check("real bmp", (common.looks_binary(bmp) or "").startswith("a BMP"), common.looks_binary(bmp))
riff = b"RIFF" + struct.pack("<I", 104) + b"WAVE" + b"x" * 100
check("real riff", (common.looks_binary(riff) or "").startswith("a RIFF"), common.looks_binary(riff))
check("nul still refused", common.looks_binary(b"te\x00xt") is not None)

# -- stdout surrogateescape round-trips undecodable bytes --
raw = b"intro \xff\xfe tail\n"
out = subprocess.run(
    [sys.executable, os.path.join(os.environ["SCRIPTS_DIR"], "clean_text.py"), "-"],
    input=raw, capture_output=True,
)
check("surrogate round-trip", b"\xff\xfe" in out.stdout, out.stdout[:40])

# -- --in-place preserves restrictive modes and exec bits --
if os.name != "nt" and hasattr(os, "chmod"):
    with tempfile.TemporaryDirectory() as td:
        for mode, label in ((0o600, "mode 0600"), (0o755, "mode 0755")):
            f = Path(td) / "t.txt"
            f.write_text("hello world\n")
            os.chmod(f, mode)
            common.safe_write_text(f, "hello world! cleaned\n")
            got = stat.S_IMODE(f.stat().st_mode)
            check(label, got == mode, oct(got))
        fresh = Path(td) / "new.txt"
        common.safe_write_text(fresh, "x\n")
        check("new file gets default", stat.S_IMODE(fresh.stat().st_mode) in (0o644, 0o664, 0o600))

# -- backup_path: next free slot, symlink refusal, src mode --
if hasattr(os, "symlink"):
    with tempfile.TemporaryDirectory() as td:
        src = Path(td) / "doc.txt"
        src.write_bytes(b"v1\n")
        bak, created = common.backup_path(src)
        check("bak1 created", created and bak.name == "doc.txt.bak" and bak.read_bytes() == b"v1\n")
        src.write_bytes(b"v2\n")
        bak2, created2 = common.backup_path(src)
        check("bak2 preserves v2", created2 and bak2.name == "doc.txt.bak2" and bak2.read_bytes() == b"v2\n", bak2.name)
        check("bak1 intact", (Path(td) / "doc.txt.bak").read_bytes() == b"v1\n")
        evil = Path(td) / "evil.txt"
        evil.write_text("x\n")
        try:
            os.symlink(Path(td) / "nonexistent-target", Path(str(evil) + ".bak"))
        except OSError:
            pass  # no symlink privilege (e.g. Windows without dev mode)
        else:
            try:
                common.backup_path(evil)
                check("dangling bak refused", False, "no SystemExit")
            except SystemExit as e:
                check("dangling bak refused", e.code == 2)

# -- tag sequences: RGI flags survive, tag-encoded text strips --
def tags(word):
    return "".join(chr(0xE0000 + ord(c)) for c in word)

eng = "\U0001F3F4" + tags("gbeng") + "\U000E007F"
out, _ = clean_text("x" + eng + "y")
check("real flag kept", eng in out)
payload = "\U0001F3F4" + tags("secret") + "\U000E007F"
out, st = clean_text("x" + payload + "y")
check("secret tags stripped", payload not in out and st["removed_count"] == 7)

# -- same-script invisible caps --
jamo = "\u1102"
fillers = "".join("\u115F" if i % 2 == 0 else "\u1160" for i in range(100))
out, st = clean_text(jamo + fillers)
kept = sum(1 for c in out if ord(c) in (0x115F, 0x1160))
check("filler cap 2", kept == 2, kept)
m = "\u1820"
out, _ = clean_text(m + "\u180B\u180C\u180D")
check("fvs cap 1", sum(1 for c in out if 0x180B <= ord(c) <= 0x180F) == 1)
rep = inspect_text(m + "\u180B\u180C\u180D")
check("excess reported", any(h.kind == "excess_script_glue" for h in rep.hits),
      [(h.kind, h.count) for h in rep.hits])
kh = "\u1780"
out, _ = clean_text(kh + "\u17B4\u17B5")
check("khmer cap 1", sum(1 for c in out if ord(c) in (0x17B4, 0x17B5)) == 1)

# -- score_stylometry --force-text parity with the other CLIs --
with tempfile.TemporaryDirectory() as td:
    f = Path(td) / "note.txt"
    f.write_bytes(b"binary \x00 blob " * 40)
    bad = subprocess.run(
        [sys.executable, os.path.join(os.environ["SCRIPTS_DIR"], "score_stylometry.py"), str(f)],
        capture_output=True,
    )
    check("score refuses binary without flag", bad.returncode == 2, bad.returncode)
    ok = subprocess.run(
        [sys.executable, os.path.join(os.environ["SCRIPTS_DIR"], "score_stylometry.py"), "--force-text", str(f)],
        capture_output=True,
    )
    check("score accepts --force-text", ok.returncode in (0, 1), ok.returncode)
    # text that merely opens with a magic prefix needs no flag at all
    f.write_bytes(b"BMW quarterly report. " * 40)
    plain = subprocess.run(
        [sys.executable, os.path.join(os.environ["SCRIPTS_DIR"], "score_stylometry.py"), str(f)],
        capture_output=True,
    )
    check("magic-prefixed text scores", plain.returncode in (0, 1), plain.returncode)

if failures:
    print("FAIL")
    for f in failures:
        print(" -", f)
    sys.exit(1)
print("PASS")
`
	driverPath := filepath.Join(t.TempDir(), "driver.py")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(py, driverPath)
	cmd.Env = append(os.Environ(), "SCRIPTS_DIR="+scripts)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vendored skill script checks failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("unexpected driver output:\n%s", out)
	}
}

// TestWatermarksServicePin verifies the cloned service checkout is verified
// against a trusted commit pin before any code runs.
func TestWatermarksServicePin(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	base := t.TempDir()
	if _, err := Materialize(base); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	script := filepath.Join(base, "skills", "remove-ai-marks", "scripts", "oxidean-watermarks-service.sh")

	// Local upstream stand-in: one commit tagged v0.7.0.
	upstream := filepath.Join(t.TempDir(), "upstream")
	mustRun := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(filepath.Join(upstream, "service", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "service", "scripts", "server.py"), []byte("print('ok')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(upstream, "git", "init", "-q")
	mustRun(upstream, "git", "add", ".")
	mustRun(upstream, "git", "commit", "-qm", "init")
	commit := mustRun(upstream, "git", "rev-parse", "HEAD")
	mustRun(upstream, "git", "tag", "v0.7.0")

	// Wrong pin: clone must be refused before any service code runs.
	root := t.TempDir()
	cmd := exec.Command(bash, script, "ensure")
	cmd.Env = append(os.Environ(),
		"OXIDEAN_ROOT="+root,
		"WATERMARKS_CHECKOUT="+filepath.Join(root, "tmp", "watermarks-remover"),
		"WATERMARKS_CLONE_URL="+upstream,
		"WATERMARKS_SERVICE_URL=http://127.0.0.1:1",
		"WATERMARKS_PIN_COMMIT=0000000000000000000000000000000000000000",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("ensure succeeded with wrong pin:\n%s", out)
	}
	if !strings.Contains(string(out), "expected") && !strings.Contains(string(out), "refusing") {
		t.Fatalf("expected pin-mismatch refusal, got:\n%s", out)
	}

	// Correct pin: verification passes; the run may then fail health-check
	// (stub server exits) but must not fail on pin grounds.
	root2 := t.TempDir()
	cmd = exec.Command(bash, script, "ensure")
	cmd.Env = append(os.Environ(),
		"OXIDEAN_ROOT="+root2,
		"WATERMARKS_CHECKOUT="+filepath.Join(root2, "tmp", "watermarks-remover"),
		"WATERMARKS_CLONE_URL="+upstream,
		"WATERMARKS_SERVICE_URL=http://127.0.0.1:1",
		"WATERMARKS_PIN_COMMIT="+commit,
	)
	out, _ = cmd.CombinedOutput()
	if strings.Contains(string(out), "expected") || strings.Contains(string(out), "unverified") {
		t.Fatalf("pin verification failed with matching commit:\n%s", out)
	}
}
