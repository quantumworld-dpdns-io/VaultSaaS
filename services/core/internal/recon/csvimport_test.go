package recon

import (
	"strings"
	"testing"
)

func TestParseExpectedPayments(t *testing.T) {
	csv := "\uFEFFReference,Coin,Address,Amount,Expected_By\n" +
		"INV-1,tbtc4,tb1qabc,1000,2026-10-31\n" +
		"INV-2,HTETH,0xAbCdEf0000000000000000000000000000000000,2500000000000000000,2026-10-12T00:00:00Z\n" +
		"INV-3,tbtc4,tb1qdef,42,\n"
	ps, errs, err := ParseExpectedPayments(strings.NewReader(csv))
	if err != nil || len(errs) != 0 || len(ps) != 3 {
		t.Fatalf("got %d payments, errs %v, err %v", len(ps), errs, err)
	}
	if ps[1].Coin != "hteth" || ps[1].Address != "0xabcdef0000000000000000000000000000000000" {
		t.Fatalf("normalisation: %+v", ps[1])
	}
	if ps[0].ExpectedBy == nil || ps[0].ExpectedBy.UTC().Format("2006-01-02T15:04") != "2026-10-31T15:59" {
		t.Fatalf("date-only expected_by should be end of day in Taipei, got %v", ps[0].ExpectedBy)
	}
	if ps[2].ExpectedBy != nil {
		t.Fatal("empty expected_by must stay nil")
	}
}

func TestParseRejectsBadRows(t *testing.T) {
	csv := "reference,coin,address,amount\n" +
		"A,tbtc4,addr,1.5\n" + // float amount
		"B,tbtc4,addr,-3\n" + // negative
		"C,TB TC,addr,1\n" + // bad coin
		"D,tbtc4,=cmd|' /C calc'!A0,1\n" + // formula-like address
		"E,tbtc4,addr,1\n" +
		"E,tbtc4,addr,1\n" // duplicate reference
	_, errs, err := ParseExpectedPayments(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 5 {
		t.Fatalf("want 5 line errors, got %v", errs)
	}
	if _, _, err := ParseExpectedPayments(strings.NewReader("reference,amount\nx,1\n")); err == nil {
		t.Fatal("missing columns must be rejected")
	}
}
