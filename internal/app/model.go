package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const Version = "0.1.0-alpha.2-dev"

type Node struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Outbound map[string]any `json:"outbound"`
}
type Subscription struct {
	ID        string    `json:"id,omitempty"`
	URL       string    `json:"url"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
	Nodes     []Node    `json:"nodes"`
	Warnings  []string  `json:"warnings,omitempty"`
}
type State struct {
	Schema             int                   `json:"schema"`
	Enabled            bool                  `json:"enabled"`
	Policy             string                `json:"policy"`
	Selected           string                `json:"selected"`
	Subscription       Subscription          `json:"subscription"`
	Failover           FailoverConfig        `json:"failover"`
	SavedSubscriptions []SubscriptionProfile `json:"savedSubscriptions,omitempty"`
}
type Event struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}
type Device struct {
	Mode         string  `json:"mode"`
	Architecture string  `json:"architecture"`
	LAN          string  `json:"lan"`
	WAN          string  `json:"wan"`
	LANAddress   string  `json:"lanAddress"`
	Checks       []Check `json:"checks"`
}
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type PublicNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type View struct {
	Version            string               `json:"version"`
	Mode               string               `json:"mode"`
	Enabled            bool                 `json:"enabled"`
	Running            bool                 `json:"running"`
	Policy             string               `json:"policy"`
	Selected           string               `json:"selected"`
	Nodes              []PublicNode         `json:"nodes"`
	SubscriptionName   string               `json:"subscriptionName"`
	UpdatedAt          time.Time            `json:"updatedAt"`
	Warnings           []string             `json:"warnings"`
	Events             []Event              `json:"events"`
	Device             Device               `json:"device"`
	Failover           FailoverConfig       `json:"failover"`
	Health             HealthStatus         `json:"health"`
	Subscriptions      []PublicSubscription `json:"subscriptions"`
	ActiveSubscription string               `json:"activeSubscription"`
	MaxSubscriptions   int                  `json:"maxSubscriptions"`
}

func DefaultState() State {
	return State{Schema: 2, Policy: "rule", Subscription: Subscription{Nodes: []Node{}}}
}
func (s State) Node() (Node, error) {
	for _, n := range s.Subscription.Nodes {
		if n.ID == s.Selected {
			return n, nil
		}
	}
	return Node{}, errors.New("请先选择一个可用节点")
}
func (s State) Validate() error {
	if err := s.validateSubscriptions(); err != nil {
		return err
	}
	if err := s.validateFailover(); err != nil {
		return err
	}
	if s.Policy != "rule" && s.Policy != "global" && s.Policy != "direct" {
		return errors.New("未知策略")
	}
	if s.Enabled && s.Policy != "direct" {
		_, err := s.Node()
		return err
	}
	return nil
}
func nodeID(out map[string]any) string {
	// Name is deliberately excluded so provider renames don't break selection.
	// Include connection parameters to keep distinct accounts/transports separate.
	// Changed credentials intentionally require a fresh selection before activation.
	b, _ := json.Marshal(out)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:10])
}
func AtomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func LoadState(dir string) (State, error) {
	s := DefaultState()
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, errors.New("配置文件损坏，请先从备份恢复")
	}
	if s.Schema != 1 && s.Schema != 2 {
		return s, errors.New("不支持的配置版本")
	}
	s.normalizeSubscriptions()
	return s, s.Validate()
}
func SaveState(dir string, s State) error {
	s.normalizeSubscriptions()
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(dir, "state.json"), b)
}
