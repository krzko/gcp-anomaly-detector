package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v2"
)

type Anomaly struct {
	MetricName string
	Value      float64
	Timestamp  time.Time
	Message    string
}

type Config struct {
	Metrics          []string          `yaml:"metrics"`
	PollingTime      int               `yaml:"polling_time"` // in seconds
	ProjectID        string            `yaml:"project_id"`
	BaselineDuration int               `yaml:"baseline_duration"` // in days
	RecentDuration   int               `yaml:"recent_duration"`   // in minutes
	Filters          map[string]string `yaml:"filters"`           // map of metric to filter string
	ZScoreThreshold  float64           `yaml:"z_score_threshold"` // Z-score threshold for anomaly detection
}

type SimpleAnomalyDetector struct {
	metricsStats map[string]MetricStats
	initialised  bool
	zScores      map[string]float64
}

type MetricStats struct {
	mean          float64
	stddev        float64
	currentMean   float64
	currentStdDev float64
}

// LoadConfig loads the configuration from a YAML file
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (d *SimpleAnomalyDetector) GetBaseline(metrics []*monitoringpb.TimeSeries) {
	log.Println("Initialising baseline...")

	d.metricsStats = make(map[string]MetricStats)

	for _, metric := range metrics {
		metricType := metric.Metric.Type

		var sum float64
		var count float64
		for _, point := range metric.Points {
			value := point.Value.GetDoubleValue()
			sum += value
			count++
		}
		if count == 0 {
			log.Printf("No data points for metric: %s. Skipping...\n", metricType)
			continue
		}
		mean := sum / count

		var sumOfSquares float64
		for _, point := range metric.Points {
			value := point.Value.GetDoubleValue()
			deviation := value - mean
			sumOfSquares += deviation * deviation
		}
		stddev := math.Sqrt(sumOfSquares / count)

		d.metricsStats[metricType] = MetricStats{
			mean:   mean,
			stddev: stddev,
		}

		log.Printf("Baseline for metric %s: Mean: %.2f, StdDev: %.2f\n", metricType, mean, stddev)
	}

	d.initialised = true
	log.Println("Baseline initialised.")
}

func (d *SimpleAnomalyDetector) DetectAnomalies(metrics []*monitoringpb.TimeSeries, zScoreThreshold float64) ([]Anomaly, error) {
	if !d.initialised {
		return nil, errors.New("baseline not initialised")
	}

	var anomalies []Anomaly
	d.zScores = make(map[string]float64)
	for _, metric := range metrics {
		metricType := metric.Metric.Type
		stats, ok := d.metricsStats[metricType]
		if !ok {
			log.Printf("No baseline stats for metric: %s. Skipping...\n", metricType)
			continue
		}
		log.Printf("Detecting anomalies for metric: %s...\n", metricType)
		for _, point := range metric.Points {
			value := point.Value.GetDoubleValue()
			zScore := (value - stats.mean) / stats.stddev
			d.zScores[fmt.Sprintf("%s at %s", metricType, point.Interval.EndTime.AsTime())] = zScore // Store zScore
			if math.Abs(zScore) > zScoreThreshold {
				anomaly := Anomaly{
					MetricName: metricType,
					Value:      value,
					Timestamp:  point.Interval.EndTime.AsTime(),
					Message:    fmt.Sprintf("Value deviates significantly from the mean (Z-score: %.2f)", zScore),
				}
				anomalies = append(anomalies, anomaly)
			}
		}
	}

	// Log all Z-scores for debugging
	for metricTime, zScore := range d.zScores {
		log.Printf("Z-score for %s: %.2f\n", metricTime, zScore)
	}

	log.Printf("%d anomalies detected.\n", len(anomalies))
	return anomalies, nil
}

func (d *SimpleAnomalyDetector) UpdateCurrentStats(metrics []*monitoringpb.TimeSeries) {
	for _, metric := range metrics {
		metricType := metric.Metric.Type

		var sum float64
		var count float64
		for _, point := range metric.Points {
			value := point.Value.GetDoubleValue()
			sum += value
			count++
		}
		if count == 0 {
			log.Printf("No data points for metric: %s in the current run. Skipping...\n", metricType)
			continue
		}
		currentMean := sum / count

		var sumOfSquares float64
		for _, point := range metric.Points {
			value := point.Value.GetDoubleValue()
			deviation := value - currentMean
			sumOfSquares += deviation * deviation
		}
		currentStdDev := math.Sqrt(sumOfSquares / count)

		// Update the metric's statistics in the metricsStats map
		stats := d.metricsStats[metricType]
		stats.currentMean = currentMean
		stats.currentStdDev = currentStdDev
		d.metricsStats[metricType] = stats

		log.Printf("Current run statistics for metric %s updated. Mean: %.2f, StdDev: %.2f\n", metricType, currentMean, currentStdDev)
	}
}

func main() {
	log.Println("Loading configuration...")
	config, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Set default baseline duration if not provided
	if config.BaselineDuration == 0 {
		config.BaselineDuration = 7
	}

	log.Println("Creating monitoring client...")
	client, err := monitoring.NewMetricClient(context.Background())
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	log.Println("Fetching historical metrics...")
	historicalMetrics, err := fetchHistoricalMetrics(client, config.ProjectID, config.Metrics, config.BaselineDuration, config.Filters)
	if err != nil {
		log.Fatalf("Failed to fetch historical metrics: %v", err)
	}

	detector := &SimpleAnomalyDetector{}
	detector.GetBaseline(historicalMetrics)

	processMetrics(client, config, detector)

	pollingInterval := time.Duration(config.PollingTime) * time.Second
	ticker := time.NewTicker(pollingInterval)
	log.Printf("Starting polling every %v...\n", pollingInterval)

	for range ticker.C {
		processMetrics(client, config, detector)
	}
}

func processMetrics(client *monitoring.MetricClient, config *Config, detector *SimpleAnomalyDetector) {
	log.Println("Fetching recent metrics...")

	// Now using the config object to get ProjectID, Metrics, and RecentDuration
	recentMetrics, err := fetchRecentMetrics(client, config.ProjectID, config.Metrics, config.RecentDuration, config.Filters)
	if err != nil {
		log.Printf("Failed to fetch recent metrics: %v", err)
		return
	}

	// Update the current run statistics
	detector.UpdateCurrentStats(recentMetrics)

	// Log the baseline and current statistics for each metric
	for _, metric := range recentMetrics {
		metricType := metric.Metric.Type
		stats := detector.metricsStats[metricType]

		log.Printf(
			"Metric: %s, Baseline Mean: %.2f, Baseline StdDev: %.2f, Current Mean: %.2f, Current StdDev: %.2f\n",
			metricType,
			stats.mean,
			stats.stddev,
			stats.currentMean,
			stats.currentStdDev,
		)
	}

	anomalies, err := detector.DetectAnomalies(recentMetrics, config.ZScoreThreshold)
	if err != nil {
		log.Printf("Failed to detect anomalies: %v", err)
		return
	}

	for _, anomaly := range anomalies {
		fmt.Printf("Anomaly detected: %s at %s with value %.2f - %s\n",
			anomaly.MetricName, anomaly.Timestamp, anomaly.Value, anomaly.Message)
	}
}

func fetchHistoricalMetrics(client *monitoring.MetricClient, projectID string, metrics []string, baselineDuration int, filters map[string]string) ([]*monitoringpb.TimeSeries, error) {
	ctx := context.Background()
	var allTimeSeries []*monitoringpb.TimeSeries

	// Calculate the time range for the historical data
	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(baselineDuration) * 24 * time.Hour)

	log.Printf("Fetching historical metrics for project %s from %s to %s...\n", projectID, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

	for _, metric := range metrics {
		log.Printf("Fetching historical data for metric: %s...\n", metric)

		filterString := fmt.Sprintf("metric.type=\"%s\"", metric)
		if filter, exists := filters[metric]; exists {
			filterString = fmt.Sprintf("%s AND %s", filterString, filter)
		}

		req := &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + projectID,
			Filter: filterString,
			Interval: &monitoringpb.TimeInterval{
				StartTime: &timestamppb.Timestamp{Seconds: startTime.Unix()},
				EndTime:   &timestamppb.Timestamp{Seconds: endTime.Unix()},
			},
		}

		it := client.ListTimeSeries(ctx, req)
		for {
			ts, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("Failed to fetch time series data for metric %s: %v\n", metric, err)
				return nil, fmt.Errorf("could not list time series: %v", err)
			}
			allTimeSeries = append(allTimeSeries, ts)
		}
		log.Printf("Fetched historical data for metric: %s\n", metric)
	}

	log.Println("Finished fetching historical metrics.")
	return allTimeSeries, nil
}

func fetchRecentMetrics(client *monitoring.MetricClient, projectID string, metrics []string, recentDuration int, filters map[string]string) ([]*monitoringpb.TimeSeries, error) {
	ctx := context.Background()
	var allTimeSeries []*monitoringpb.TimeSeries

	// Define the time range for the recent data based on the RecentDuration config field
	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(recentDuration) * time.Minute)

	log.Printf("Fetching recent metrics for project %s from %s to %s...\n", projectID, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

	for _, metric := range metrics {
		log.Printf("Fetching recent data for metric: %s...\n", metric)

		filterString := fmt.Sprintf("metric.type=\"%s\"", metric)
		if filter, exists := filters[metric]; exists {
			filterString = fmt.Sprintf("%s AND %s", filterString, filter)
		}

		req := &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + projectID,
			Filter: filterString,
			Interval: &monitoringpb.TimeInterval{
				StartTime: &timestamppb.Timestamp{Seconds: startTime.Unix()},
				EndTime:   &timestamppb.Timestamp{Seconds: endTime.Unix()},
			},
		}

		it := client.ListTimeSeries(ctx, req)
		for {
			ts, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("Failed to fetch time series data for metric %s: %v\n", metric, err)
				return nil, fmt.Errorf("could not list time series: %v", err)
			}
			allTimeSeries = append(allTimeSeries, ts)
		}
		log.Printf("Fetched recent data for metric: %s\n", metric)
	}

	log.Println("Finished fetching recent metrics.")
	return allTimeSeries, nil
}
