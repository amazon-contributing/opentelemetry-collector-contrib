# Histograms

This package holds common CloudWatch histogram functionality for AWS owned OpenTelemetry components.

## Visualize histogram mappings

1. Remove `t.Skip(...)` from `TestWriteInputHistograms` and run the test to generate json files for the input histograms.
1. Remove `t.Skip(...)` from `TestWriteConvertedHistograms` and run the test to generate json files for the converted histograms.
1. Run `histogram_mapping.py` to generate visualizations

```bash
pip install matplotlib numpy
python histogram_mappings.py
````
