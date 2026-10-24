package schedule

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPrivateLaunchdConfig(t *testing.T) {
	args := []string{"/path with spaces/weread", "--home", "/tmp/A&B<user>", "sync", "--json"}
	b, e := Plist("test.label", args, "/tmp/A&B<user>", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		_, e = d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if !strings.Contains(string(b), "<integer>3600</integer>") || strings.Contains(string(b), "WEREAD_API_KEY") || !strings.Contains(string(b), "A&amp;B&lt;user&gt;") {
		t.Fatalf("unsafe plist: %s", b)
	}
	if _, e = Plist("label", args, "/tmp", time.Second); e == nil {
		t.Fatal("too short interval accepted")
	}
}

func TestSystemdEscaping(t *testing.T) {
	s, timer, e := Systemd("label", []string{"/bin/weread", "--home", `/tmp/100% $cash "quoted"`}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(s), `100%% $$cash \"quoted\"`) || !strings.Contains(string(timer), "OnUnitInactiveSec=3600s") {
		t.Fatalf("incorrect unit: %s %s", s, timer)
	}
	if _, _, e = Systemd("label", []string{"/bin/evil\nExecStart=other"}, time.Hour); e == nil {
		t.Fatal("unit injection accepted")
	}
}
