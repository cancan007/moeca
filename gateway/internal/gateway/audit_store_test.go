package gateway

import (
	"path/filepath"
	"testing"
)

func TestAuditStorePersistsAndVerifies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")

	st, err := OpenAuditStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := st.append(accessLog{Time: "t", Service: "anthropic", Status: 200, Session: "s"}); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := st.recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("recent = %d, want 3", len(recs))
	}
	res, err := st.verify()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Count != 3 {
		t.Fatalf("verify = %+v, want ok/3", res)
	}
	st.Close()

	// reopen: chain resumes, and new appends keep verifying
	st2, err := OpenAuditStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if err := st2.append(accessLog{Time: "t", Service: "github", Status: 201}); err != nil {
		t.Fatal(err)
	}
	res, _ = st2.verify()
	if !res.OK || res.Count != 4 {
		t.Fatalf("after reopen verify = %+v, want ok/4", res)
	}
}

func TestAuditVerifyDetectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	st, err := OpenAuditStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 3; i++ {
		st.append(accessLog{Time: "t", Service: "anthropic", Status: 200})
	}
	// tamper: edit a stored record out-of-band (simulating a DB edit)
	if _, err := st.db.Exec(`UPDATE audit_log SET record = '{"service":"forged"}' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	res, err := st.verify()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq != 2 {
		t.Fatalf("verify = %+v, want broken at seq 2", res)
	}
}
