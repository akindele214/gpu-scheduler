package benchmark

import (
	"encoding/csv"
	"os"
)

func LoadPrompts(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close() // Ensure the file is closed after reading

	// 2. Create a new CSV reader
	reader := csv.NewReader(file)

	// 3. Read all records at once
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	// 4. Iterate over the records ([][]string)
	prompts := []string{}
	for i := 1; i < len(records); i++ {
		prompts = append(prompts, records[i][1])
	}
	return prompts, nil
}
