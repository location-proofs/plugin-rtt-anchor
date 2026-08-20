import json
import pandas as pd
import matplotlib.pyplot as plt
from pathlib import Path

def load_single_measurement_file(filepath):
    data_rows = []
    path = Path(filepath)
    if not path.exists():
        print(f"File not found: {path}")
        return pd.DataFrame()
        
    with open(path, 'r') as f:
        for line in f:
            line = line.strip()
            if line:
                try:
                    record = json.loads(line)
                    record['source_file'] = path.name
                    data_rows.append(record)
                except json.JSONDecodeError:
                    continue
    return pd.DataFrame(data_rows)

files_to_load = [
    "../results/measurements_falk_to_falk_cpu.json",
    "../results/measurements_falk_to_node1_cpu.json",
    "../results/measurements_falk_to_node1_gpu.json",
    "../results/measurements_falk_to_tuusula_cpu.json",
    "../results/measurements_node1_to_node1_cpu.json",
    "../results/measurements_node1_to_node1_gpu.json",
    "../results/measurements_tuusula_to_falk_cpu.json",
    "../results/measurements_tuusula_to_node1_gpu.json",
    "../results/measurements_tuusula_to_tuusula_cpu.json"
]

# Process each measurement file individually
for file_path in files_to_load:
    try:
        print(f"\nProcessing: {file_path}")
        df = load_single_measurement_file(file_path)
        
        if df.empty:
            print(f"No measurement records found or file skipped.")
            continue
            
        print(f"Successfully loaded {len(df)} records.")
        
        # 1. Convert RTT from seconds to microseconds (µs)
        if 'anchor_measured_rtt_s' in df.columns:
            df['anchor_measured_rtt_us'] = df['anchor_measured_rtt_s'] * 1_000_000
        elif 'anchor_measured_rtt_ns' in df.columns:
            df['anchor_measured_rtt_us'] = df['anchor_measured_rtt_ns'] / 1_000
        
        # 2. Key Statistics Summary (using microseconds)
        percentiles_list = [0.25, 0.50, 0.75, 0.90, 0.95, 0.99]
        summary = df[['anchor_measured_rtt_us', 'calibrated_distance_m']].describe(percentiles=percentiles_list)
        print("\n--- Summary Statistics (RTT in µs) ---")
        print(summary)
        
        # 3. Plotting RTT Trends and Distribution in Microseconds
        fig, axes = plt.subplots(1, 2, figsize=(14, 5))

        rtt_us = df['anchor_measured_rtt_us']

        # Plot 1: RTT over sequence
        axes[0].plot(df['seq'], rtt_us, marker='.', linestyle='-', alpha=0.6, color='b')
        axes[0].set_title(f'RTT Over Sequence ({Path(file_path).stem})')
        axes[0].set_xlabel('Sequence Number')
        axes[0].set_ylabel('RTT (µs)')
        axes[0].grid(True)

        # Plot 2: RTT Distribution Histogram
        axes[1].hist(rtt_us, bins=30, color='g', alpha=0.7, edgecolor='black')
        axes[1].set_title('RTT Distribution')
        axes[1].set_xlabel('RTT (µs)')
        axes[1].set_ylabel('Frequency')
        axes[1].grid(True)

        plt.tight_layout()
        
        # Dynamically name the output PNG based on the source filename
        output_filename = f"rtt_analysis_{Path(file_path).stem}.png"
        plt.savefig(output_filename, dpi=300)
        print(f"Plot saved successfully as '{output_filename}'.")
        
        # Close the plot to free memory
        plt.close(fig)

    except Exception as e:
        print(f"Error processing {file_path}: {e}")