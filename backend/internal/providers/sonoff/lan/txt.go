package lan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// JoinTXTData concatenates mDNS TXT data1, data2, data3 and data4 in order.
// It accepts either a map[string]string, a []string of raw TXT records, or
// the four data values as strings. The variadic form keeps the helper useful
// with both common mDNS library representations.
func JoinTXTData(input ...any) string {
	values := make(map[int]string, 4)
	for _, item := range input {
		switch value := item.(type) {
		case map[string]string:
			for key, part := range value {
				if index, ok := txtDataIndex(key); ok {
					values[index] = cleanTXTValue(part)
				}
			}
		case []string:
			for _, record := range value {
				key, part, ok := splitTXTRecord(record)
				if ok {
					if index, valid := txtDataIndex(key); valid {
						values[index] = cleanTXTValue(part)
					}
				}
			}
		case string:
			// Positional strings are the convenient JoinTXTData(data1, ...)
			// form. A record such as "data2=..." is also accepted.
			if key, part, ok := splitTXTRecord(value); ok {
				if index, valid := txtDataIndex(key); valid {
					values[index] = cleanTXTValue(part)
					continue
				}
			}
			values[nextTXTIndex(values)] = value
		case nil:
			// Ignore nil optional records.
		default:
			if provider, ok := item.(interface{ TXTData() map[string]string }); ok {
				for key, part := range provider.TXTData() {
					if index, valid := txtDataIndex(key); valid {
						values[index] = cleanTXTValue(part)
					}
				}
			}
		}
	}

	var builder strings.Builder
	for index := 1; index <= 4; index++ {
		builder.WriteString(values[index])
	}
	return builder.String()
}

// JoinTXTDataParts is the typed counterpart for callers that already have
// the four records split out.
func JoinTXTDataParts(data1, data2, data3, data4 string) string {
	return JoinTXTData(data1, data2, data3, data4)
}

// TXTData exposes the TXT map through the small interface accepted by
// JoinTXTData without coupling the helper to a discovery implementation.
func (s Service) TXTData() map[string]string {
	return s.TXT
}

func txtDataIndex(key string) (int, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if len(key) != len("data1") || !strings.HasPrefix(key, "data") {
		return 0, false
	}
	index, err := strconv.Atoi(key[len("data"):])
	return index, err == nil && index >= 1 && index <= 4
}

func splitTXTRecord(record string) (string, string, bool) {
	key, value, found := strings.Cut(record, "=")
	if !found {
		return "", "", false
	}
	return strings.TrimSpace(key), value, true
}

func cleanTXTValue(value string) string {
	// Some DNS-SD libraries return "data1=value" while others expose only
	// "value". Do not duplicate the prefix when the former is supplied.
	if key, part, ok := splitTXTRecord(value); ok {
		if _, valid := txtDataIndex(key); valid {
			return part
		}
	}
	return value
}

func nextTXTIndex(values map[int]string) int {
	for index := 1; index <= 4; index++ {
		if _, exists := values[index]; !exists {
			return index
		}
	}
	return 4
}

// SortedTXT returns stable key/value records, useful when passing a parsed
// profile to a DNS-SD library that expects deterministic output.
func SortedTXT(txt map[string]string) []string {
	keys := make([]string, 0, len(txt))
	for key := range txt {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, fmt.Sprintf("%s=%s", key, txt[key]))
	}
	return result
}
