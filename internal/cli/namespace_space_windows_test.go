package cli

import "testing"

func TestMigrationFreeBytesWindowsReturnsAvailableSpace(t *testing.T) {
	free, err := migrationFreeBytes(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Fatal("available space must be non-zero")
	}
}
