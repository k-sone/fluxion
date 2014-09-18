package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/yosisa/fluxion/buffer"
	"github.com/yosisa/fluxion/event"
	"github.com/yosisa/fluxion/plugin"
)

type Config struct {
	URI                string `codec:"uri"`
	IndexName          string `codec:"index_name"`
	TypeName           string `codec:"type_name"`
	LogstashFormat     bool   `codec:"logstash_format"`
	LogstashPrefix     string `codec:"logstash_prefix"`
	LogstashDateFormat string `codec:"logstash_dateformat"`
	TagKey             string `codec:"tag_key"`
	IDKey              string `codec:"id_key"`
	ParentKey          string `codec:"parent_key"`
}

type ElasticsearchOutput struct {
	conf   *Config
	client *http.Client
}

func (o *ElasticsearchOutput) Init(f plugin.ConfigFeeder) error {
	o.conf = &Config{}
	if err := f(o.conf); err != nil {
		return err
	}
	if o.conf.TypeName == "" {
		o.conf.TypeName = "fluxion"
	}
	if o.conf.LogstashPrefix == "" {
		o.conf.LogstashPrefix = "logstash"
	}
	if o.conf.LogstashDateFormat == "" {
		o.conf.LogstashDateFormat = "2006.01.02"
	}
	return nil
}

func (o *ElasticsearchOutput) Start() error {
	o.client = &http.Client{}
	return nil
}

func (o *ElasticsearchOutput) Encode(r *event.Record) (buffer.Sizer, error) {
	index := o.conf.IndexName

	if o.conf.LogstashFormat {
		if _, ok := r.Value["@timestamp"]; !ok {
			r.Value["@timestamp"] = r.Time.Format("2006-01-02T15:04:05-07:00")
		}
		index = r.Time.Format(o.conf.LogstashPrefix + "-" + o.conf.LogstashDateFormat)
	}
	if o.conf.TagKey != "" {
		r.Value[o.conf.TagKey] = r.Tag
	}

	action := map[string]string{
		"_index": index,
		"_type":  o.conf.TypeName,
	}
	if o.conf.IDKey != "" {
		if v, ok := r.Value[o.conf.IDKey].(string); ok {
			action["_id"] = v
		}
	}
	if o.conf.ParentKey != "" {
		if v, ok := r.Value[o.conf.ParentKey].(string); ok {
			action["_parent"] = v
		}
	}

	b1, err := json.Marshal(map[string]interface{}{"index": action})
	if err != nil {
		return nil, err
	}
	b2, err := json.Marshal(r.Value)
	if err != nil {
		return nil, err
	}
	b := append(b1, '\n')
	b = append(b, b2...)
	b = append(b, '\n')
	return buffer.BytesItem(b), nil
}

func (o *ElasticsearchOutput) Write(l []buffer.Sizer) (int, error) {
	var rs []io.Reader
	for _, b := range l {
		rs = append(rs, bytes.NewReader(b.(buffer.BytesItem)))
	}

	resp, err := o.client.Post(o.conf.URI, "application/json", io.MultiReader(rs...))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	b, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("Error %s, body: %s", resp.Status, b)
	}

	return len(l), nil
}

func main() {
	plugin.New(&ElasticsearchOutput{}).Run()
}
