# capacity_pricing.star - Utilization-based capacity pricing template.
#
# Assigns price tiers based on utilization relative to capacity from reference
# data. Uses three tiers:
#   - off_peak:  utilization < 50% of capacity
#   - standard:  utilization 50-85% of capacity
#   - peak:      utilization >= 85% of capacity
#
# ForecastContext builtins used: avg, add_seconds
#
# Reference data attributes (required):
#   - capacity: Maximum capacity value (numeric).
#
# Reference data attributes (optional):
#   - off_peak_rate:  Price rate for off-peak tier (default: 0.5)
#   - standard_rate:  Price rate for standard tier (default: 1.0)
#   - peak_rate:      Price rate for peak tier (default: 2.0)
#   - base_price:     Base price multiplied by rate (default: 100.0)
#
# Input: Historical utilization observations.
# Output: Forecast points with tier-based pricing.

def compute_forecast(ctx):
    ref = ctx["reference_data"]
    if ref == None:
        return []

    attrs = ref["attributes"]
    capacity = float(attrs["capacity"])
    if capacity <= 0:
        return []

    # Configurable rates with defaults
    off_peak_rate = float(attrs["off_peak_rate"]) if "off_peak_rate" in attrs else 0.5
    standard_rate = float(attrs["standard_rate"]) if "standard_rate" in attrs else 1.0
    peak_rate = float(attrs["peak_rate"]) if "peak_rate" in attrs else 2.0
    base_price = float(attrs["base_price"]) if "base_price" in attrs else 100.0

    obs_dict = ctx["observations"]
    dataset_keys = sorted(obs_dict.keys())
    if len(dataset_keys) == 0:
        return []

    obs = obs_dict[dataset_keys[0]]
    if len(obs) == 0:
        return []

    # Compute average utilization from historical data
    # avg() returns a Decimal; convert via str() for float arithmetic
    values = [float(o["value"]) for o in obs]
    avg_utilization = float(str(avg(values)))

    utilization_ratio = avg_utilization / capacity

    # Determine price tier
    if utilization_ratio >= 0.85:
        rate = peak_rate
        tier = "peak"
    elif utilization_ratio >= 0.50:
        rate = standard_rate
        tier = "standard"
    else:
        rate = off_peak_rate
        tier = "off_peak"

    price = base_price * rate

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts = add_seconds(now, offset)
        points.append({
            "timestamp": ts,
            "value": price,
            "metadata": {
                "algorithm": "capacity_pricing",
                "tier": tier,
                "utilization_ratio": str(utilization_ratio),
            },
        })
    return points
