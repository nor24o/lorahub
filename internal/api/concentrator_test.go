package api

import "testing"

func TestEngineErrors(t *testing.T) {
	log := `Opening SPI communication interface
INFO: using legacy timestamp
ERROR: FDD mode of Radio A has not been set properly
ERROR: failed to start AGC firmware
ERROR: lgw_start() failed.
Opening SPI communication interface
ERROR: FDD mode of Radio A has not been set properly
ERROR: failed to start AGC firmware
ERROR: lgw_start() failed.`
	want := "ERROR: FDD mode of Radio A has not been set properly · ERROR: failed to start AGC firmware · ERROR: lgw_start() failed."
	if got := engineErrors(log); got != want {
		t.Fatalf("%q", got)
	}
	if engineErrors("all fine\n") != "" {
		t.Fatal("no errors expected")
	}
}
