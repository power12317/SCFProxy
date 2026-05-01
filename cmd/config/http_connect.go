package config

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

type HttpConnectRecord struct {
	Port int
	Url  string
}

type HttpConnectConfig struct {
	mu      sync.RWMutex
	Records map[string]map[string]*HttpConnectRecord
}

func LoadHttpConnectConfig() (*HttpConnectConfig, error) {
	conf := &HttpConnectConfig{Records: make(map[string]map[string]*HttpConnectRecord)}
	data, err := os.ReadFile(HttpConnectProxyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return conf, nil
		}
		return nil, err
	}

	err = json.Unmarshal(data, &conf.Records)
	return conf, err
}

func (c *HttpConnectConfig) Get(provider, region string) (*HttpConnectRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.Records[provider][region]
	return record, ok
}

func (c *HttpConnectConfig) Set(provider, region string, record *HttpConnectRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.Records[provider]; !ok {
		c.Records[provider] = make(map[string]*HttpConnectRecord)
	}
	c.Records[provider][region] = record
}

func (c *HttpConnectConfig) Delete(provider, region string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Records[provider], region)
}

func (c *HttpConnectConfig) Save() error {
	return save(c.Records, HttpConnectProxyPath)
}

type HttpConnectRecordWithInfo struct {
	Provider string
	Region   string
	*HttpConnectRecord
}

func (c *HttpConnectConfig) GetAllRecordsWithInfo() []*HttpConnectRecordWithInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var records []*HttpConnectRecordWithInfo
	for provider, rmap := range c.Records {
		for region, record := range rmap {
			if record.Url != "" {
				records = append(records, &HttpConnectRecordWithInfo{
					Provider:          provider,
					Region:            region,
					HttpConnectRecord: record,
				})
			}
		}
	}
	return records
}

func (c *HttpConnectConfig) ToDoubleArray() [][]string {
	data := [][]string{}
	for provider, rmap := range c.Records {
		for region, record := range rmap {
			data = append(data, []string{provider, region, record.Url})
		}
	}
	return data
}
