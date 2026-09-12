// scripts/pdatafix/main_test.go
//
// The test that matters is not "is it sorted afterwards" -- a sort that threw
// the relocations away would pass that. It is that every record still points
// at the same function it pointed at before, which is checked by carrying an
// identifying value through the shuffle and asserting the pairing survives.
package main

import (
	"encoding/binary"
	"testing"
)

// buildObject assembles a minimal x86-64 COFF object with one .pdata section
// holding len(order) records. Record i names symbol i, whose value is
// order[i] -- so passing an unsorted slice produces an unsorted object.
func buildObject(order []uint32) []byte {
	n := len(order)
	const (
		hdr     = coffHeaderSize
		secOff  = hdr
		rawSize = 0
	)
	pdataSize := n * runtimeFuncSize
	nrel := n * 3

	rawPtr := secOff + sectionSize
	relPtr := rawPtr + pdataSize
	symPtr := relPtr + nrel*relocSize
	total := symPtr + n*symbolSize

	obj := make([]byte, total)
	binary.LittleEndian.PutUint16(obj[0:], machineAMD64)
	binary.LittleEndian.PutUint16(obj[2:], 1) // one section
	binary.LittleEndian.PutUint32(obj[8:], uint32(symPtr))
	binary.LittleEndian.PutUint32(obj[12:], uint32(n))
	binary.LittleEndian.PutUint16(obj[16:], 0) // no optional header

	s := obj[secOff:]
	copy(s[0:8], ".pdata")
	binary.LittleEndian.PutUint32(s[16:], uint32(pdataSize))
	binary.LittleEndian.PutUint32(s[20:], uint32(rawPtr))
	binary.LittleEndian.PutUint32(s[24:], uint32(relPtr))
	binary.LittleEndian.PutUint16(s[32:], uint16(nrel))

	for i := 0; i < n; i++ {
		// BeginAddress stays 0 as Go emits it; EndAddress carries a marker so
		// the test can tell which record is which after the sort.
		rec := obj[rawPtr+i*runtimeFuncSize:]
		binary.LittleEndian.PutUint32(rec[4:], marker(order[i]))

		for f := 0; f < 3; f++ {
			r := obj[relPtr+(i*3+f)*relocSize:]
			binary.LittleEndian.PutUint32(r[0:], uint32(i*runtimeFuncSize+f*4))
			binary.LittleEndian.PutUint32(r[4:], uint32(i))
			binary.LittleEndian.PutUint16(r[8:], 3) // ADDR32NB
		}
		binary.LittleEndian.PutUint32(obj[symPtr+i*symbolSize+8:], order[i])
		obj[symPtr+i*symbolSize+17] = 0 // no aux entries
	}
	return obj
}

func marker(v uint32) uint32 { return v ^ 0xA5A5A5A5 }

// readBack returns, for each record position, the sort key its relocation
// resolves to and the marker stored in the record body. They must agree.
func readBack(t *testing.T, obj []byte, n int) []uint32 {
	t.Helper()
	s := obj[coffHeaderSize:]
	rawPtr := int(binary.LittleEndian.Uint32(s[20:]))
	relPtr := int(binary.LittleEndian.Uint32(s[24:]))
	symPtr := int(binary.LittleEndian.Uint32(obj[8:]))

	byOffset := map[uint32]int{}
	for i := 0; i < n*3; i++ {
		r := obj[relPtr+i*relocSize:]
		byOffset[binary.LittleEndian.Uint32(r[0:])] = int(binary.LittleEndian.Uint32(r[4:]))
	}

	keys := make([]uint32, n)
	for i := 0; i < n; i++ {
		symIdx, ok := byOffset[uint32(i*runtimeFuncSize)]
		if !ok {
			t.Fatalf("record %d lost its BeginAddress relocation", i)
		}
		key := binary.LittleEndian.Uint32(obj[symPtr+symIdx*symbolSize+8:])
		got := binary.LittleEndian.Uint32(obj[rawPtr+i*runtimeFuncSize+4:])
		if got != marker(key) {
			t.Fatalf("record %d: body says %#x, relocation resolves to %#x -- "+
				"the record and its relocations were separated",
				i, got, marker(key))
		}
		keys[i] = key
	}
	return keys
}

func TestSortsAndKeepsRelocationsWithTheirRecords(t *testing.T) {
	order := []uint32{0x400, 0x100, 0x900, 0x200, 0x700, 0x050, 0x800, 0x300}
	obj := buildObject(order)

	n, inversions, err := sortPdata(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(order) {
		t.Fatalf("counted %d records, want %d", n, len(order))
	}
	if inversions == 0 {
		t.Fatal("fixture was supposed to be out of order")
	}

	keys := readBack(t, obj, n)
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("still unsorted at %d: %#x > %#x", i, keys[i-1], keys[i])
		}
	}
}

func TestAlreadySortedIsLeftAlone(t *testing.T) {
	order := []uint32{0x100, 0x200, 0x300, 0x400}
	obj := buildObject(order)
	before := append([]byte(nil), obj...)

	_, inversions, err := sortPdata(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	if inversions != 0 {
		t.Fatalf("reported %d inversions in a sorted fixture", inversions)
	}
	if string(before) != string(obj) {
		t.Error("a sorted object was modified")
	}
}

func TestRunningTwiceChangesNothing(t *testing.T) {
	order := []uint32{0x900, 0x100, 0x500, 0x050}
	obj := buildObject(order)

	if _, _, err := sortPdata(obj, false); err != nil {
		t.Fatal(err)
	}
	once := append([]byte(nil), obj...)
	if _, inv, err := sortPdata(obj, false); err != nil {
		t.Fatal(err)
	} else if inv != 0 {
		t.Fatalf("second pass found %d inversions", inv)
	}
	if string(once) != string(obj) {
		t.Error("a second pass changed the object")
	}
}

func TestCheckModeReportsWithoutWriting(t *testing.T) {
	order := []uint32{0x300, 0x100, 0x200}
	obj := buildObject(order)
	before := append([]byte(nil), obj...)

	_, inversions, err := sortPdata(obj, true)
	if err != nil {
		t.Fatal(err)
	}
	if inversions == 0 {
		t.Fatal("check mode missed the inversion")
	}
	if string(before) != string(obj) {
		t.Error("check mode modified the object")
	}
}

func TestRejectsAMismatchedRelocationCount(t *testing.T) {
	obj := buildObject([]uint32{0x200, 0x100})
	// Claim one relocation too few.
	binary.LittleEndian.PutUint16(obj[coffHeaderSize+32:], 5)
	if _, _, err := sortPdata(obj, false); err == nil {
		t.Error("expected an error on a relocation count that does not match")
	}
}
