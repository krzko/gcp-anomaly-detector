# gcp-anomaly-detector

This tool is designed to detect anomalies in Google Cloud Monitoring metrics. It fetches historical and recent metrics data, computes baseline statistics, and identifies anomalies based on Z-score analysis. The Z-score represents how many standard deviations an element is from the mean, and a high absolute Z-score indicates a potential anomaly.

## Configuration

The tool is configured using a YAML file. Here's an example configuration file with explanations for each field:

```yaml
metrics:
  - 'custom.googleapis.com/otel/foo_connection_count'
  - 'custom.googleapis.com/otel/foo_current_connections'  # List of metric types to monitor
filters:
  custom.googleapis.com/otel/foo_connection_count: 'resource.type="generic_task" AND metric.labels."environment"="dev"'
  custom.googleapis.com/otel/foo_current_connections": 'resource.type="generic_task" AND metric.labels."environment"="dev"'  # Filters to apply when fetching metrics
baseline_duration: 7  # Baseline duration in days
polling_time: 60  # Polling time in seconds
project_id: foo-bar-dev-1a2b3c  # GCP Project ID
recent_duration: 60  # Recent metrics duration in minutes
z_score_threshold: 3.00  # Z-score threshold for anomaly detection

```

## Usage

1. Create a configuration file following the example above.
2. Build the tool:

```sh
go build
```

3. Authenticate to Google Cloud

```sh
gcloud auth login --update-ad

export GOOGLE_APPLICATION_CREDENTIALS="/Users/$USER/.config/gcloud/application_default_credentials.json"
```

4. Run the tool:

```sh
./gcp-anomaly-detector
```

The tool will load the configuration, initialise a baseline using historical metrics data, and start polling recent metrics data at the specified interval. It will log the Z-scores for each metric and report any anomalies detected based on the configured Z-score threshold.

## Understanding Z-Score

The Z-score is a statistical measurement that describes a value's relationship to the mean of a group of values. It is measured in terms of standard deviations from the mean. In this tool, a high absolute Z-score (e.g., 3.0 or -3.0) indicates a potential anomaly.

* A positive Z-score indicates the data point is higher than the mean.
* A negative Z-score indicates the data point is lower than the mean.

The `z_score_threshold` in the configuration file determines the Z-score value at which a data point is considered an anomaly. For example, with a `z_score_threshold` of 3.00, any data point with a Z-score of 3.0 or -3.0 and above would be flagged as an anomaly.
