package proc

import (
	"os"
	"testing"
)

// В Go тесты живут в том же пакете (package proc).
// Каждая тестовая функция начинается с Test и принимает *testing.T.
// Аналог в C#: [Test] методы в NUnit/xUnit.

func TestFSReader_ReadRSS_CurrentProcess(t *testing.T) {
	reader := New()
	pid := os.Getpid() // PID текущего процесса (самого теста)

	rss, err := reader.ReadRSS(pid)

	// В Go нет Assert.Equal — проверяем условия вручную и вызываем t.Fatal/t.Error.
	// t.Fatal — прерывает тест. t.Error — помечает как failed, но продолжает.
	if err != nil {
		t.Fatalf("ReadRSS(%d) unexpected error: %v", pid, err)
	}
	if rss <= 0 {
		t.Errorf("ReadRSS(%d) = %d, expected > 0", pid, rss)
	}
}

func TestFSReader_IsAlive_CurrentProcess(t *testing.T) {
	reader := New()
	pid := os.Getpid()

	if !reader.IsAlive(pid) {
		t.Errorf("IsAlive(%d) = false, expected true — current process must be alive", pid)
	}
}

func TestFSReader_IsAlive_NonExistentPID(t *testing.T) {
	reader := New()

	// PID 999999999 заведомо не существует
	if reader.IsAlive(999999999) {
		t.Error("IsAlive(999999999) = true, expected false")
	}
}

func TestFSReader_FindByMask_FindsCurrentProcess(t *testing.T) {
	reader := New()

	// "go" есть в cmdline любого go test процесса
	processes, err := reader.FindByMask("go")
	if err != nil {
		t.Fatalf("FindByMask unexpected error: %v", err)
	}
	if len(processes) == 0 {
		t.Fatal("FindByMask(\"go\") returned 0 processes, expected at least 1")
	}

	// Проверяем что наш PID есть в результатах
	pid := os.Getpid()
	found := false
	for _, p := range processes {
		if p.PID == pid {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FindByMask(\"go\") did not include current process PID %d", pid)
	}
}

func TestMatchMask(t *testing.T) {
	cmdline := "/usr/bin/php8.2 artisan horizon:work redis --name=default --supervisor=yuboost:supervisor-ai-generation --queue=ai"

	cases := []struct {
		mask string
		want bool
	}{
		{"horizon:work", true},                          // простая подстрока
		{"horizon:work*--queue=ai", true},               // wildcard
		{"horizon:work*--supervisor=*supervisor-ai-*", true}, // двойной wildcard
		{"horizon:work*--queue=critical", false},        // не та очередь
		{"queue:work", false},                           // другая маска
	}

	for _, c := range cases {
		got := matchMask(c.mask, cmdline)
		if got != c.want {
			t.Errorf("matchMask(%q) = %v, want %v", c.mask, got, c.want)
		}
	}
}

func TestFSReader_FindByMask_NoMatchReturnsEmpty(t *testing.T) {
	reader := New()

	// Маска которая точно не матчит ни один процесс
	processes, err := reader.FindByMask("__no_such_process_mask_xyz__")
	if err != nil {
		t.Fatalf("FindByMask unexpected error: %v", err)
	}
	if len(processes) != 0 {
		t.Errorf("FindByMask with no matches returned %d processes, expected 0", len(processes))
	}
}
