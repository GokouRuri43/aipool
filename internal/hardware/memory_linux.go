package hardware

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func detectMemoryMB() (uint64, uint64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	var total, available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kilobytes >> 10
		case "MemAvailable:":
			available = kilobytes >> 10
		}
	}
	return total, available
}
