package opentsdbfirehosenozzle_test

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/pivotal-cf-experimental/opentsdb-firehose-nozzle/nozzleconfig"
	"github.com/pivotal-cf-experimental/opentsdb-firehose-nozzle/opentsdbfirehosenozzle"
	"github.com/pivotal-cf-experimental/opentsdb-firehose-nozzle/poster"
	. "github.com/pivotal-cf-experimental/opentsdb-firehose-nozzle/testhelpers"
	"github.com/pivotal-cf-experimental/opentsdb-firehose-nozzle/uaatokenfetcher"
	"github.com/pivotal-cf-experimental/opentsdb-firehose-nozzle/util"

)

var _ = Describe("OpenTSDB Firehose Nozzle", func() {
	var fakeUAA *FakeUAA
	var fakeFirehose *FakeFirehose
	var fakeOpenTSDB *FakeOpenTSDB
	var config *nozzleconfig.NozzleConfig
	var nozzle *opentsdbfirehosenozzle.OpenTSDBFirehoseNozzle
	var logOutput *gbytes.Buffer
	var tokenFetcher *uaatokenfetcher.UAATokenFetcher

	BeforeEach(func() {
		fakeUAA = NewFakeUAA("bearer", "123456789")
		fakeToken := fakeUAA.AuthToken()
		fakeFirehose = NewFakeFirehose(fakeToken)
		fakeOpenTSDB = NewFakeOpenTSDB()

		fakeUAA.Start()
		fakeFirehose.Start()
		fakeOpenTSDB.Start()

		tokenFetcher = &uaatokenfetcher.UAATokenFetcher{
			UaaUrl: fakeUAA.URL(),
		}

		config = &nozzleconfig.NozzleConfig{
			UAAURL:               fakeUAA.URL(),
			OpenTSDBURL:          fakeOpenTSDB.URL(),
			FlushDurationSeconds: 10,
			TrafficControllerURL: strings.Replace(fakeFirehose.URL(), "http:", "ws:", 1),
			DisableAccessControl: false,
			MetricPrefix:         "opentsdb.nozzle.",
			FirehoseReconnectDelay: 100000000,
		}

		logOutput = gbytes.NewBuffer()
		log.SetOutput(logOutput)
		nozzle = opentsdbfirehosenozzle.NewOpenTSDBFirehoseNozzle(config, tokenFetcher)
	})

	AfterEach(func() {
		fakeUAA.Close()
		fakeFirehose.Close()
		fakeOpenTSDB.Close()
	})

	It("sends metrics when the FlushDurationTicker ticks", func() {
		fakeFirehose.KeepConnectionAlive()
		defer fakeFirehose.CloseAliveConnection()
		config.FlushDurationSeconds = 1
		nozzle = opentsdbfirehosenozzle.NewOpenTSDBFirehoseNozzle(config, tokenFetcher)
		envelope := events.Envelope{
		Origin:    proto.String("origin"),
			EventType: events.Envelope_ValueMetric.Enum(),
			ValueMetric: &events.ValueMetric{
			Name:  proto.String("metricName"),
				Value: proto.Float64(float64(1)),
				Unit:  proto.String("gauge"),
			},
			Deployment: proto.String("deployment-name"),
			Job:        proto.String("doppler"),
			Timestamp: proto.Int64(1000000000),
		}
		fakeFirehose.AddEvent(envelope)
		go nozzle.Start()
		defer nozzle.Stop()
		var contents []byte
		Eventually(fakeOpenTSDB.ReceivedContents, 2).Should(Receive(&contents))
		var metrics []poster.Metric
		err := json.Unmarshal(util.UnzipIgnoreError(contents), &metrics)
		Expect(err).ToNot(HaveOccurred())
		Expect(logOutput).ToNot(gbytes.Say("Error while reading from the firehose"))

		// +3 internal metrics that show totalMessagesReceived, totalMetricSent, and totalFirehoseDisconnects
		Expect(metrics).To(HaveLen(4))
	})

	It("receives data from the firehose", func(done Done) {
		defer close(done)

		for i := 0; i < 10; i++ {
			envelope := events.Envelope{
				Origin:    proto.String("origin"),
				Timestamp: proto.Int64(1000000000),
				EventType: events.Envelope_ValueMetric.Enum(),
				ValueMetric: &events.ValueMetric{
					Name:  proto.String(fmt.Sprintf("metricName-%d", i)),
					Value: proto.Float64(float64(i)),
					Unit:  proto.String("gauge"),
				},
				Deployment: proto.String("deployment-name"),
				Job:        proto.String("doppler"),
				Index:      proto.String("0"),
			}
			fakeFirehose.AddEvent(envelope)
		}

		go nozzle.Start()
		defer nozzle.Stop()

		var contents []byte
		Eventually(fakeOpenTSDB.ReceivedContents).Should(Receive(&contents))

		var metrics []poster.Metric
		err := json.Unmarshal(util.UnzipIgnoreError(contents), &metrics)
		Expect(err).ToNot(HaveOccurred())

		// +3 internal metrics that show totalMessagesReceived, totalMetricSent, and totalFirehoseDisconnects
		Expect(metrics).To(HaveLen(13))

	}, 2)

	It("gets a valid authentication token", func() {
		go nozzle.Start()
		defer nozzle.Stop()
		Eventually(fakeFirehose.Requested).Should(BeTrue())
		Consistently(fakeFirehose.LastAuthorization).Should(Equal("bearer 123456789"))
	})

	Context("when the DisableAccessControl is set to true", func() {
		var tokenFetcher *FakeTokenFetcher

		BeforeEach(func() {
			fakeUAA = NewFakeUAA("", "")
			fakeToken := fakeUAA.AuthToken()
			fakeFirehose = NewFakeFirehose(fakeToken)
			fakeOpenTSDB = NewFakeOpenTSDB()
			tokenFetcher = &FakeTokenFetcher{}

			fakeUAA.Start()
			fakeFirehose.Start()
			fakeOpenTSDB.Start()

			config = &nozzleconfig.NozzleConfig{
				FlushDurationSeconds: 1,
				OpenTSDBURL:          fakeOpenTSDB.URL(),
				TrafficControllerURL: strings.Replace(fakeFirehose.URL(), "http:", "ws:", 1),
				DisableAccessControl: true,
				FirehoseReconnectDelay: 100000000,
			}

			nozzle = opentsdbfirehosenozzle.NewOpenTSDBFirehoseNozzle(config, tokenFetcher)
		})

		AfterEach(func() {
			fakeUAA.Close()
			fakeFirehose.Close()
			fakeOpenTSDB.Close()
		})

		It("still tries to connect to the firehose", func() {
			go nozzle.Start()
			defer nozzle.Stop()
			Eventually(fakeFirehose.Requested).Should(BeTrue())
		})

		It("gets an empty authentication token", func() {
			go nozzle.Start()
			defer nozzle.Stop()
			Consistently(fakeUAA.Requested).Should(Equal(false))
			Consistently(fakeFirehose.LastAuthorization).Should(Equal(""))
		})

		It("does not require the presence of config.UAAURL", func() {
			go nozzle.Start()
			defer nozzle.Stop()

			Consistently(func() int {
				return tokenFetcher.NumCalls
			}).Should(Equal(0))
		})
	})

	Context("when the firehose disconnects", func() {
		BeforeEach(func() {
			nozzle = opentsdbfirehosenozzle.NewOpenTSDBFirehoseNozzle(config, tokenFetcher)
		})

		It("Increments the total disconnects metric", func() {
			go nozzle.Start()
			defer nozzle.Stop()
			fakeFirehose.Close()

			var contents []byte
			Eventually(fakeOpenTSDB.ReceivedContents).Should(Receive(&contents))

			var metrics []poster.Metric
			err := json.Unmarshal(util.UnzipIgnoreError(contents), &metrics)
			Expect(err).ToNot(HaveOccurred())

			// +3 internal metrics that show totalMessagesReceived, totalMetricSent, and totalFirehoseDisconnects
			Expect(metrics).To(HaveLen(3))
			metric := getDisconnectMetric(metrics)
			Expect(metric.Metric).To(Equal("opentsdb.nozzle.totalFirehoseDisconnects"))
			Expect(metric.Value).To(BeEquivalentTo(1.0))
		})
	})


})

func getDisconnectMetric(metrics []poster.Metric) poster.Metric {
	for _, metric := range metrics {
		if metric.Metric == "opentsdb.nozzle.totalFirehoseDisconnects" {
			return metric
		}
	}
	return poster.Metric{};
}