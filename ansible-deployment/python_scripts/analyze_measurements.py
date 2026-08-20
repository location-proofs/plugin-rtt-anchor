import json
import glob
import pandas as pd
import matplotlib.pyplot as plt

def load_measurement_files(file_pattern="*.json"):
    data_rows = []
    for filepath in glob.glob(file_pattern):
        with open(filepath, 'r') as f:
            for line in f:
                line = line.strip()
                if line:
                    try:
                        record = json.loads(line)
                        record['source_file'] = filepath
                        data_rows.append(record)
                    except json.JSONDecodeError:
                        continue
    return pd.DataFrame(data_rows)

# 1. Load all measurement JSON files
df = load_measurement_files("../results/measurements_falk_to_node1_gpu.json")

if df.empty:
    print("No measurement records found! Check your file path or extension.")
else:
    print(f"Successfully loaded {len(df)} total records from files.")
    
    # 2. Key Statistics Summary
    percentiles_list = [0.25, 0.50, 0.75, 0.90, 0.95, 0.99]

    summary = df[['anchor_measured_rtt_s', 'calibrated_distance_m']].describe(percentiles=percentiles_list)
    print("\n--- Detailed Summary Statistics with 99th Percentile ---")
    print(summary)
    
    # 3. Plotting RTT Trends and Distribution
    fig, axes = plt.subplots(1, 2, figsize=(14, 5))

    # Convert RTT to milliseconds for easier reading if it's stored in seconds
    rtt_s = df['anchor_measured_rtt_s'] if 'anchor_measured_rtt_s' in df else df['anchor_measured_rtt_ns'] / 1e9
    rtt_ms = rtt_s * 1000

    # Plot 1: RTT over sequence
    axes[0].plot(df['seq'], rtt_ms, marker='.', linestyle='-', alpha=0.6, color='b')
    axes[0].set_title('Anchor Measured RTT Over Sequence')
    axes[0].set_xlabel('Sequence Number')
    axes[0].set_ylabel('RTT (ms)')
    axes[0].grid(True)

    # Plot 2: RTT Distribution Histogram
    axes[1].hist(rtt_ms, bins=30, color='g', alpha=0.7, edgecolor='black')
    axes[1].set_title('RTT Distribution')
    axes[1].set_xlabel('RTT (ms)')
    axes[1].set_ylabel('Frequency')
    axes[1].grid(True)

    plt.tight_layout()
    plt.savefig('rtt_analysis.png', dpi=300)
    print("\nPlot saved successfully as 'rtt_analysis.png'.")
    plt.show()