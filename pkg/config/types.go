package config

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
)

// Config struct
type Config struct {
	Name                  string                 `json:"name"`
	JobServiceAccount     string                 `json:"jobServiceAccount"`
	JobNodeSelector       map[string]string      `json:"jobNodeSelector"`
	RunOnUnscheduledNodes bool                   `json:"runOnUnscheduledNodes"`
	CronExpression        string                 `json:"cronExpression"`
	ReportDirectory       string                 `json:"reportDirectory"`
	ReportHistory         int                    `json:"reportHistory"`
	PodPoolSize           int                    `json:"podPoolSize"`
	RunOnStartup          bool                   `json:"runOnStartup"`
	Metrics               Metrics                `json:"metrics"`
	Custom                map[string]interface{} `json:"custom"`
	CallbackServiceName   string                 `json:"callbackServiceName"`
	CallbackServicePort   int                    `json:"callbackServicePort"`

	Namespace      string         `json:"-"`
	JobPodTemplate string         `json:"-"`
	Owner          runtime.Object `json:"-"`
}

// PodName get the name of the pod
func (cfg *Config) PodName(nodeName string, id string) string {
	nameParts := strings.Split(nodeName, ".")
	podName := fmt.Sprintf("%s-job-%s-%s", cfg.Name, nameParts[0], id)
	return podName
}

// Metrics config
type Metrics struct {
	Prefix string            `json:"prefix"`
	Gauges map[string]Metric `json:"gauges"`
}

// NameFor get the name of a metric
func (m *Metrics) NameFor(name string) string {
	return fmt.Sprintf("%s_%s", m.Prefix, name)
}

// Metric config
type Metric struct {
	Help   string   `json:"help"`
	Labels []string `json:"labels"`
}
