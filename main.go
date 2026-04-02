package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Global   GlobalConfig    `yaml:"global"`
	Requests []RequestConfig `yaml:"requests"`
}

type GlobalConfig struct {
	Variables map[string]string `yaml:"variables"`
}

type RequestConfig struct {
	Name       string            `yaml:"name"`
	URL        string            `yaml:"url"`
	Method     string            `yaml:"method"`
	Headers    map[string]string `yaml:"headers"`
	Validation ValidationConfig  `yaml:"validation"`
}

type ValidationConfig struct {
	Status int    `yaml:"status"`
	Match  string `yaml:"match"`
}

func interpolate(text string, vars map[string]string) string {
	re := regexp.MustCompile(`\${(\w+)}`)
	return re.ReplaceAllStringFunc(text, func(m string) string {
		key := m[2 : len(m)-1]
		if val, ok := vars[key]; ok {
			return val
		}
		return m
	})
}

func executeRequest(reqCfg RequestConfig, client *http.Client, vars map[string]string, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	url := interpolate(reqCfg.URL, vars)
	method := strings.ToUpper(reqCfg.Method)
	if method == "" {
		method = "GET"
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		fmt.Printf("✘ %s [Error: %v]\n", reqCfg.Name, err)
		return
	}

	for k, v := range reqCfg.Headers {
		req.Header.Set(k, interpolate(v, vars))
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("✘ %s [Network Error: %v]\n", reqCfg.Name, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	success := true
	var failureReason string

	if reqCfg.Validation.Status != 0 && resp.StatusCode != reqCfg.Validation.Status {
		success = false
		failureReason = fmt.Sprintf("Expected status %d, got %d", reqCfg.Validation.Status, resp.StatusCode)
	}

	if success && reqCfg.Validation.Match != "" {
		matchRegex, err := regexp.Compile(reqCfg.Validation.Match)
		if err != nil {
			success = false
			failureReason = "Invalid regex in validation"
		} else if !matchRegex.Match(body) {
			success = false
			failureReason = "Body did not match regex"
		}
	}

	if success {
		fmt.Printf("✓ %s [%d %s]\n", reqCfg.Name, resp.StatusCode, http.StatusText(resp.StatusCode))
	} else {
		fmt.Printf("✘ %s [%s]\n", reqCfg.Name, failureReason)
	}
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		os.Exit(1)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		fmt.Printf("Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	skipTLS, _ := strconv.ParseBool(config.Global.Variables["SKIP_TLS_VERIFY"])
	timeoutStr := config.Global.Variables["TIMEOUT"]
	timeout := 30 * time.Second
	if timeoutStr != "" {
		if t, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = t
		}
	}

	isParallel, _ := strconv.ParseBool(config.Global.Variables["PARALLEL"])

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLS},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	if isParallel {
		var wg sync.WaitGroup
		for _, r := range config.Requests {
			wg.Add(1)
			go executeRequest(r, client, config.Global.Variables, &wg)
		}
		wg.Wait()
	} else {
		for _, r := range config.Requests {
			executeRequest(r, client, config.Global.Variables, nil)
		}
	}
}
