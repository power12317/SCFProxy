package config

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

type HttpRecord struct {
	Port int    // 保留字段，用于兼容
	Url  string
}

type HttpConfig struct {
	mu      sync.RWMutex
	Records map[string]map[string]*HttpRecord
}

func LoadHttpConfig() (*HttpConfig, error) {
	conf := &HttpConfig{Records: make(map[string]map[string]*HttpRecord)}
	data, err := os.ReadFile(HttpProxyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return conf, nil
		}
		return nil, err
	}

	err = json.Unmarshal(data, &conf.Records)
	return conf, err
}

func (c *HttpConfig) Get(provider, region string) (*HttpRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.Records[provider][region]
	return record, ok
}

func (c *HttpConfig) Set(provider, region string, record *HttpRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.Records[provider]
	if !ok {
		c.Records[provider] = make(map[string]*HttpRecord)
	}
	c.Records[provider][region] = record
}

func (c *HttpConfig) Delete(provider, region string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Records[provider], region)
}

func (c *HttpConfig) Save() error {
	return save(c.Records, HttpProxyPath)
}

func (c *HttpConfig) AvailableApis() []string {
	return c.GetAllUrls()
}

// 新增方法：获取所有URL
func (c *HttpConfig) GetAllUrls() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var urls []string
	for _, rmap := range c.Records {
		for _, record := range rmap {
			if record.Url != "" {
				urls = append(urls, record.Url)
			}
		}
	}
	return urls
}

// 获取所有记录（包含端口信息）
func (c *HttpConfig) GetAllRecords() []*HttpRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var records []*HttpRecord
	for _, rmap := range c.Records {
		for _, record := range rmap {
			if record.Url != "" {
				records = append(records, record)
			}
		}
	}
	return records
}

// HttpRecordWithInfo 包含完整信息的记录
type HttpRecordWithInfo struct {
	Provider string
	Region   string
	*HttpRecord
}

// 获取所有记录（包含 provider、region 和端口信息）
func (c *HttpConfig) GetAllRecordsWithInfo() []*HttpRecordWithInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var records []*HttpRecordWithInfo
	for provider, rmap := range c.Records {
		for region, record := range rmap {
			if record.Url != "" {
				records = append(records, &HttpRecordWithInfo{
					Provider:   provider,
					Region:     region,
					HttpRecord: record,
				})
			}
		}
	}
	return records
}

func (c *HttpConfig) ToDoubleArray() [][]string {
	data := [][]string{}
	for provider, rmap := range c.Records {
		for region, record := range rmap {
			data = append(data, []string{provider, region, record.Url})
		}
	}
	return data
}
