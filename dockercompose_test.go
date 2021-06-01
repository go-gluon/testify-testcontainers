package containers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDockerComposeLoad(t *testing.T) {
	dc := &DockerCompose{}
	err := dc.load("tests/dc-test1.yml")
	assert.Nil(t, err)
	assert.EqualValues(t, 3, len(dc.Services))

	s := dc.Services["postgres"]
	assert.NotNil(t, s)
	assert.EqualValues(t, "postgres:10.5", s.Image)
}

func TestNewContainerConfig(t *testing.T) {
	dc := &DockerCompose{}
	err := dc.load("tests/dc-test1.yml")
	assert.Nil(t, err)

	name := "postgres"
	s := dc.Services[name]
	assert.NotNil(t, s)
	config, err := newContainerConfig(name, s)
	assert.Nil(t, err)
	assert.NotNil(t, config)
	assert.EqualValues(t, "postgres:10.5", config.Service.Image)
}

type TestRunSuite struct {
	ContainerTestEnvironment
}

func TestRunSuiteEnvironment(t *testing.T) {
	RunFile(t, new(TestRunSuite), "tests/dc-run-test.yml")
}

func (d *TestRunSuite) AfterSetupContainers() error {
	d.Log("After setup containers")
	return nil
}

func (d *TestRunSuite) BeforeTest(suiteName, testName string) {
	d.Logf("!!!!!!!!! BEFORE TEST %v - %v\n", suiteName, testName)
}

func (d *TestRunSuite) AfterTest(suiteName, testName string) {
	d.Logf("!!!!!!!!! AFTER TEST %v - %v\n", suiteName, testName)
}

func (d *TestRunSuite) TestDatabasePort() {
	p := d.ServicePort("postgres", "5432")
	d.Logf("Database port %v\n", p)
	d.NotEqual(5432, p)
}

func (d *TestRunSuite) TestWiremockPort() {
	p := d.ServicePort("mocking-server", "8080")
	d.Logf("Wiremock port %v\n", p)
	d.NotEqual(8080, p)
}

type TestRunMultipleSuite struct {
	ContainerTestEnvironment
}

func TestRunMultipleSuiteEnvironment(t *testing.T) {
	RunFile(t, new(TestRunMultipleSuite), "tests/dc-run-multiple-dockers.yml")
}

func (d *TestRunMultipleSuite) Test1() {
	services := []string{"postgres", "mocking-server", "mocking-server1", "mocking-server2"}

	for _, service := range services {
		s, b := d.Service(service)
		d.True(b)
		d.NotNil(s)
	}

	s, b := d.Service("custom")
	d.False(b)
	d.Nil(s)
}
