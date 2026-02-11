# moving_average.star - Simple/Exponential moving average forecast template.
#
# Computes a simple moving average over all available historical observations
# and projects it across the forecast horizon. Supports configurable smoothing
# via exponential weighting when reference data provides an alpha parameter.
#
# ForecastContext builtins used: avg, add_seconds
#
# Reference data attributes (optional):
#   - alpha: Exponential smoothing factor (0-1). When absent, uses simple average.
#
# Input: Historical observations from the first input dataset.
# Output: Forecast points at each granularity step within the horizon.

def compute_forecast(ctx):
    obs_dict = ctx["observations"]

    # Use the first available dataset
    dataset_keys = sorted(obs_dict.keys())
    if len(dataset_keys) == 0:
        return []

    obs = obs_dict[dataset_keys[0]]
    if len(obs) == 0:
        return []

    # Extract numeric values from observations
    values = [float(o["value"]) for o in obs]

    # Check for exponential smoothing alpha in reference data
    ref = ctx["reference_data"]
    alpha = 0.0
    if ref != None:
        attrs = ref["attributes"]
        if "alpha" in attrs:
            alpha = float(attrs["alpha"])

    if alpha > 0.0 and alpha <= 1.0:
        # Exponential moving average: weight recent values more heavily
        ema = values[0]
        for i in range(1, len(values)):
            ema = alpha * values[i] + (1.0 - alpha) * ema
        forecast_value = ema
    else:
        # Simple moving average
        forecast_value = avg(values)

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    if granularity <= 0:
        return []
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts = add_seconds(now, offset)
        points.append({
            "timestamp": ts,
            "value": forecast_value,
            "metadata": {"algorithm": "moving_average"},
        })
    return points
