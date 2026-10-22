package lsp

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The corpus lines come from a scratch Rust program that runs the real url
// crate (2.5.8, the version the editor is built with):
//
//	f <TAB> hex(path bytes) <TAB> hex(from_file_path) | hex(from_directory_path)   ("-" for Err)
//	u <TAB> hex(uri)        <TAB> hex(Url::parse(uri)?.to_file_path())             ("-" for Err)
//
// testdata/fileurl_golden.tsv holds a few hundred of them. The full run
// (COIGUE_URL_CORPUS=path/to/url_corpus.tsv) used 84,797.

func decodeHex(t *testing.T, s string) string {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func optional(t *testing.T, s string) (string, bool) {
	if s == "-" {
		return "", false
	}
	return decodeHex(t, s), true
}

func checkURLCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<22)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 3 {
			t.Fatalf("bad corpus line %q", scanner.Text())
		}
		input := decodeHex(t, fields[1])
		checked++
		switch fields[0] {
		case "f":
			wantURI, wantDir, _ := strings.Cut(fields[2], "|")
			gotURI, gotOK := fileURI(input)
			gotDir, gotDirOK := directoryURI(input)
			want, wantOK := optional(t, wantURI)
			wantD, wantDirOK := optional(t, wantDir)
			if gotURI != want || gotOK != wantOK || gotDir != wantD || gotDirOK != wantDirOK {
				mismatches++
				if mismatches <= 20 {
					t.Errorf("path %q: fileURI = %q,%v directoryURI = %q,%v; Rust %q,%v %q,%v",
						input, gotURI, gotOK, gotDir, gotDirOK, want, wantOK, wantD, wantDirOK)
				}
			}
		case "u":
			want, wantOK := optional(t, fields[2])
			got, gotOK := uriToPath(input)
			if got != want || gotOK != wantOK {
				mismatches++
				if mismatches <= 20 {
					t.Errorf("uri %q: uriToPath = %q,%v; Rust %q,%v", input, got, gotOK, want, wantOK)
				}
			}
		default:
			t.Fatalf("bad corpus line %q", scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checked, mismatches
}

func TestFileURLAgreesWithUrlCrateGolden(t *testing.T) {
	checked, _ := checkURLCorpus(t, "testdata/fileurl_golden.tsv")
	if checked < 300 {
		t.Fatalf("only %d golden cases", checked)
	}
}

func TestFileURLAgreesWithUrlCrateCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_URL_CORPUS")
	if path == "" {
		t.Skip("COIGUE_URL_CORPUS not set")
	}
	checked, mismatches := checkURLCorpus(t, path)
	t.Logf("%d cases, %d mismatches", checked, mismatches)
}

// Hosts with characters above U+007F: the idna crate's answer for 80 sampled
// single characters, measured with "file://a<c>b/C:/x" (a valid host is
// dropped when a drive-letter path follows; an invalid one fails the parse).
// The remaining approximations are listed with uriToPath's domain code.
func TestFileURLHostsAboveASCIIAgreeWithUrlCrate(t *testing.T) {
	checked, _ := checkURLCorpus(t, "testdata/fileurl_hosts_golden.tsv")
	if checked != 80 {
		t.Fatalf("%d cases", checked)
	}
}
