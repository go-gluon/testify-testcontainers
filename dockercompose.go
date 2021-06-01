package containers

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v2"
)

const (
	PRIORITY_DEFAULT = 100
)

type Service struct {
	ContainerName string            `yaml:"container_name"`
	Image         string            `yaml:"image"`
	Environment   map[string]string `yaml:"environment"`
	Labels        []string          `yaml:"labels"`
	Ports         []string          `yaml:"ports"`
	Networks      []string          `yaml:"networks"`
	Command       []string          `yaml:"command"`
	Volumes       []string          `yaml:"volumes"`
}
type DockerCompose struct {
	Version  string              `yaml:"version"`
	Services map[string]*Service `yaml:"services"`
}

func (d *DockerCompose) load(file string) error {
	yamlFile, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(yamlFile, d)
	if err != nil {
		return err
	}
	return nil
}

type Log struct {
	value string
	times int
}

type Port struct {
	value   string
	startup time.Duration
}

type Wait struct {
	log  Log
	port Port
}

type ContainerConfig struct {
	Name            string
	Service         *Service
	Commands        []string
	AlwaysPullImage bool
	Priority        int
	ShowLog         bool
	wait            Wait
	Volumes         map[string]string
	Ports           map[string]string
	LabelProp       map[string]string
	LabelEnv        map[string]string
}

func newContainerConfig(name string, service *Service) (*ContainerConfig, error) {

	config := ContainerConfig{
		Name:            name,
		Service:         service,
		AlwaysPullImage: false,
		Priority:        PRIORITY_DEFAULT,
		ShowLog:         true,
		Ports:           list2map(service.Ports, ":"),
		Volumes:         list2map(service.Volumes, ":"),
		wait:            Wait{},
		LabelProp:       map[string]string{},
		LabelEnv:        map[string]string{},
	}
	labels := list2map(service.Labels, "=")
	if len(labels) > 0 {

		// show logs
		log, err := labelBool(labels, "test.log", config.ShowLog)
		if err != nil {
			return nil, err
		}
		config.ShowLog = log

		// priority
		pio, err := labelInt(labels, "test.priority", config.Priority)
		if err != nil {
			return nil, err
		}
		config.Priority = pio

		//wait for port
		waitPortValue, _ := labelString(labels, "test.wait.port.value", "")
		if len(waitPortValue) > 0 {
			tmp, _ := labelString(labels, "test.wait.log.startup", "60s")
			startup, err := time.ParseDuration(tmp)
			if err != nil {
				return nil, err
			}
			config.wait.port = Port{value: waitPortValue, startup: startup}
		}

		//wait for logs
		waitLogValue, _ := labelString(labels, "test.wait.log.value", "")
		if len(waitLogValue) > 0 {
			waitLogTimes, err := labelInt(labels, "test.wait.log.times", 1)
			if err != nil {
				return nil, err
			}
			config.wait.log = Log{value: waitLogValue, times: waitLogTimes}
		}

		// image pull
		imagePull, _ := labelBool(labels, "test.image.always-pull-image", false)
		config.AlwaysPullImage = imagePull

		// properties
		for k, val := range labels {

			tmp, exists := labelProp(k, "test.property.")
			if exists {
				config.LabelProp[tmp] = val
				continue
			}
			tmp, exists = labelProp(k, "test.env.")
			if exists {
				config.LabelEnv[tmp] = val
				continue
			}
		}
	}

	return &config, nil
}

func labelProp(key string, suffix string) (string, bool) {
	if strings.HasPrefix(key, suffix) {
		return strings.TrimSuffix(key, suffix), true
	}
	return "", false
}

func labelString(labels map[string]string, key string, defaultValue string) (string, error) {
	val, e := labels[key]
	if !e {
		return defaultValue, nil
	}
	return val, nil
}

func labelInt(labels map[string]string, key string, defaultValue int) (int, error) {
	val, e := labels[key]
	if !e {
		return defaultValue, nil
	}
	tmp, err := strconv.Atoi(val)
	if err != nil {
		return 0, err
	}
	return tmp, nil
}

func labelBool(labels map[string]string, key string, defaultValue bool) (bool, error) {
	val, e := labels[key]
	if !e {
		return defaultValue, nil
	}
	tmp, err := strconv.ParseBool(val)
	if err != nil {
		return false, err
	}
	return tmp, nil
}

func list2map(input []string, sep string) map[string]string {
	result := map[string]string{}
	if len(input) > 0 {
		for _, item := range input {
			tmp := strings.Split(item, sep)
			if len(tmp) > 0 {
				result[tmp[0]] = tmp[1]
			}
		}
	}
	return result
}

var logRegex = regexp.MustCompile(`((\r?\n)|(\r))$`)

type DockerService struct {
	config    *ContainerConfig
	container testcontainers.Container
	ws        wait.Strategy
}

func (d *DockerService) Port(ctx context.Context, port string) (int, error) {

	np, err := d.container.MappedPort(ctx, nat.Port(port))
	if err != nil {
		return 0, err
	}
	return np.Int(), err
}

func (d *DockerService) Accept(l testcontainers.Log) {
	tmp := logRegex.ReplaceAllString(string(l.Content), ``)
	log.Printf(`[%s] %s`, d.config.Name, tmp)
}

func createContainer(ctx context.Context, config *ContainerConfig, network testcontainers.Network) (testcontainers.Container, error) {

	n := network.(*testcontainers.DockerNetwork)

	ports := []string{}
	for _, p := range config.Ports {
		ports = append(ports, p+"/tcp")
	}
	networkName := n.Name

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        config.Service.Image,
			ExposedPorts: ports,
			Networks:     []string{networkName},
			NetworkAliases: map[string][]string{
				networkName: {config.Name},
			},
			AutoRemove: true,
		},
	}
	req.AlwaysPullImage = config.AlwaysPullImage
	req.Env = config.Service.Environment

	return testcontainers.GenericContainer(ctx, req)
}

func (d *DockerService) start(ctx context.Context) error {
	// add log listener
	d.container.FollowOutput(d)

	// start container
	err := d.container.Start(ctx)
	if err != nil {
		return err
	}
	// attach the logs
	err = d.container.StartLogProducer(ctx)
	if err != nil {
		return err
	}
	// wait for logs, ports, ....
	if d.ws != nil {
		if err := d.ws.WaitUntilReady(ctx, d.container); err != nil {
			return err
		}
	}

	shortID := d.container.GetContainerID()[:12]
	fmt.Printf("Container is ready id: %s image: %s. Wait: %v\n", shortID, d.config.Service.Image, d.ws)

	return nil
}

func (d *DockerService) stop(ctx context.Context) error {
	err := d.container.StopLogProducer()
	if err != nil {
		fmt.Printf("Error stop docker service log producer %v\n", err)
	}
	return d.container.Terminate(ctx)
}

func Run(t *testing.T, te TestEnvironment) {
	RunFile(t, te, "docker-compose.yml")
}

func RunFile(t *testing.T, te TestEnvironment, file string) {

	if v, e := te.(BeforeSetupContainers); e {
		te.self().beforeSetupContainers = v
	}
	if v, e := te.(AfterSetupContainers); e {
		te.self().afterSetupContainers = v
	}
	if v, e := te.(AfterTerminateContainers); e {
		te.self().afterTerminateContainers = v
	}
	if v, e := te.(BeforeTerminateContainers); e {
		te.self().beforeTerminateContainers = v
	}
	te.self().file = file
	suite.Run(t, te.(suite.TestingSuite))
}

type TestEnvironment interface {
	self() *ContainerTestEnvironment
}

type BeforeSetupContainers interface {
	BeforeSetupContainers() error
}

type AfterSetupContainers interface {
	AfterSetupContainers() error
}

type BeforeTerminateContainers interface {
	BeforeTerminateContainers() error
}

type AfterTerminateContainers interface {
	AfterTerminateContainers() error
}

type ContainerTestEnvironment struct {
	suite.Suite
	dc                        *DockerCompose
	file                      string
	network                   testcontainers.Network
	containers                map[string]*DockerService
	groups                    map[int][]*DockerService
	sequence                  []int
	beforeSetupContainers     BeforeSetupContainers
	afterSetupContainers      AfterSetupContainers
	beforeTerminateContainers BeforeTerminateContainers
	afterTerminateContainers  AfterTerminateContainers
}

func (d *ContainerTestEnvironment) self() *ContainerTestEnvironment {
	return d
}

func (d *ContainerTestEnvironment) File() string {
	return d.file
}

func (d *ContainerTestEnvironment) DockerCompose() DockerCompose {
	return *d.dc
}

func (d *ContainerTestEnvironment) Logf(format string, args ...interface{}) {
	d.T().Logf(format, args...)
}

func (d *ContainerTestEnvironment) Log(format string) {
	d.T().Log(format)
}

func (d *ContainerTestEnvironment) Fatal(args ...interface{}) {
	d.T().Fatal(args...)
}

func (d *ContainerTestEnvironment) Fatalf(format string, args ...interface{}) {
	d.T().Fatalf(format, args...)
}

func (d *ContainerTestEnvironment) init() error {
	d.dc = &DockerCompose{}
	err := d.dc.load(d.file)
	if err != nil {
		return err
	}

	ctx := context.Background()
	d.network, err = testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{
			CheckDuplicate: true,
			Name:           uuid.New().String(),
		},
	})
	if err != nil {
		return err
	}

	if len(d.dc.Services) == 0 {
		return errors.New("no docker compose services")
	}

	d.containers = map[string]*DockerService{}
	d.groups = map[int][]*DockerService{}

	for k, v := range d.dc.Services {
		config, err := newContainerConfig(k, v)
		if err != nil {
			return err
		}

		container, err := createContainer(ctx, config, d.network)
		if err != nil {
			return err
		}

		var ws wait.Strategy
		if len(config.wait.port.value) > 0 {
			ws = wait.ForListeningPort(nat.Port(config.wait.port.value)).WithStartupTimeout(time.Duration(config.wait.port.startup) * time.Second)
		} else if len(config.wait.log.value) > 0 {
			ws = ForLog(config.wait.log.value).WithOccurrence(config.wait.log.times)
		}

		ds := &DockerService{
			config:    config,
			container: container,
			ws:        ws,
		}

		items, e := d.groups[ds.config.Priority]
		if !e {
			items = []*DockerService{}
		}
		items = append(items, ds)
		d.groups[ds.config.Priority] = items

		d.containers[k] = ds
	}

	// sort sequence
	d.sequence = []int{}
	for k := range d.groups {
		d.sequence = append(d.sequence, k)
	}
	sort.Ints(d.sequence)

	return nil
}

func (d *ContainerTestEnvironment) TearDownSuite() {

	// call setup containers method
	if d.beforeTerminateContainers != nil {
		err := d.beforeTerminateContainers.BeforeTerminateContainers()
		if err != nil {
			d.T().Error(err)
		}
	}

	// stop containers
	err := d.stop()
	if err != nil {
		d.T().Error(err)
	}

	// call setup containers method
	if d.afterTerminateContainers != nil {
		err := d.afterTerminateContainers.AfterTerminateContainers()
		if err != nil {
			d.T().Error(err)
		}
	}
}

func (d *ContainerTestEnvironment) SetupSuite() {

	// load docker compose configuration
	err := d.init()
	if err != nil {
		d.T().Fatal(err)
	}

	// call setup containers method
	if d.beforeSetupContainers != nil {
		err := d.beforeSetupContainers.BeforeSetupContainers()
		if err != nil {
			d.T().Fatal(err)
		}
	}

	// start containers
	err = d.start()
	if err != nil {
		d.T().Fatal(err)
	}

	// call setup containers method
	if d.afterSetupContainers != nil {
		err := d.afterSetupContainers.AfterSetupContainers()
		if err != nil {
			d.T().Fatal(err)
		}
	}
}

func (d *ContainerTestEnvironment) start() error {
	d.T().Logf("Start containers for test. Number: %v", len(d.containers))
	if len(d.containers) > 0 {
		ctx := context.Background()

		chErr := make(chan error)
		defer close(chErr)

		count := 0
		for _, seq := range d.sequence {
			d.T().Logf(`Start containers of priotiy: %v`, seq)

			var wg sync.WaitGroup
			count = 0

			for _, container := range d.groups[seq] {

				count++
				wg.Add(1)

				go func(container *DockerService) {
					defer wg.Done()
					d.T().Logf(`Start container: %v`, container.config.Name)
					err := container.start(ctx)
					if err != nil {
						d.T().Error(err)
					}
				}(container)

			}

			select {
			case err := <-chErr:
				d.T().Fatal(err)
			default:
				wg.Wait()
			}
		}
	}
	return nil
}

func (d *ContainerTestEnvironment) stop() error {

	d.T().Logf("Stop containers. Number: %v", len(d.containers))
	if len(d.containers) > 0 {
		ctx := context.Background()

		chErr := make(chan error)
		defer close(chErr)

		var wg sync.WaitGroup
		count := 0

		for _, container := range d.containers {

			count++
			wg.Add(1)

			go func(container *DockerService) {
				defer wg.Done()
				d.T().Logf(`Stop container: %v`, container.config.Name)
				err := container.stop(ctx)
				if err != nil {
					d.T().Error(err)
					chErr <- err
				}
			}(container)
		}
		select {
		case err := <-chErr:
			d.T().Fatal(err)
		default:
			wg.Wait()
		}
	}
	return nil
}

func (d *ContainerTestEnvironment) Service(name string) (*DockerService, bool) {
	v, e := d.containers[name]
	return v, e
}

func (d *ContainerTestEnvironment) ServicePort(name, port string) int {
	v, e := d.Service(name)
	if !e {
		d.T().Fatal("service not found", name)
	}
	ctx := context.Background()
	p, err := v.Port(ctx, port)
	if err != nil {
		d.T().Fatal(err)
	}
	return p
}
