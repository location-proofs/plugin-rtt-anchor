import json
import pandas as pd
import matplotlib.pyplot as plt
from pathlib import Path
import numpy as np

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
    "../results/measurements_node1_to_node1_cpu.json",
    "../results/measurements_tuusula_to_node1_gpu.json",
    "../results/measurements_falk_to_node1_cpu.json",
    "../results/measurements_node1_to_node1_gpu.json",
    "../results/measurements_tuusula_to_tuusula_cpu.json",
    "../results/measurements_falk_to_node1_gpu.json",
    "../results/measurements_tuusula_to_falk_cpu.json",
    "../results/measurements_falk_to_tuusula_cpu.json",
    "../results/measurements_tuusula_to_node1_cpu.json"
]

master_summary_list = []

for file_path in files_to_load:
    try:
        print(f"\nProcessing: {file_path}")
        df = load_single_measurement_file(file_path)
        
        if df.empty:
            print("No measurement records found or file skipped.")
            continue
            
        # 1. Convert RTT to microseconds (µs)
        if 'anchor_measured_rtt_s' in df.columns:
            df['anchor_measured_rtt_us'] = df['anchor_measured_rtt_s'] * 1_000_000
        elif 'anchor_measured_rtt_ns' in df.columns:
            df['anchor_measured_rtt_us'] = df['anchor_measured_rtt_ns'] / 1_000
            
        # --- ENHANCED CONVERGENCE & FLOOR VALIDATION CHECK ---
        # Step A: Drop warm-up/burn-in burn packets (e.g., first 30 samples)
        warmup_cutoff = 30
        if len(df) > warmup_cutoff:
            df_eval = df.iloc[warmup_cutoff:].copy()
        else:
            df_eval = df.copy()

        # Step B: Bucket into 10 chunks for spread analysis
        num_buckets = 10
        rtt_values = df_eval['anchor_measured_rtt_us'].values
        chunks = np.array_split(rtt_values, num_buckets)
        bucket_mins = [chunk.min() for chunk in chunks if len(chunk) > 0]
        
        min_spread = max(bucket_mins) - min(bucket_mins) if bucket_mins else 0.0
        
        # Step C: Expanding minimum trailing slope analysis (checking if floor dropped in the final 10%)
        df['running_min_us'] = df['anchor_measured_rtt_us'].expanding().min()
        split_idx = int(len(df) * 0.9)
        floor_stable_at_end = True
        if len(df) > 10 and split_idx > 0:
            early_min = df['running_min_us'].iloc[split_idx]
            final_min = df['running_min_us'].iloc[-1]
            if final_min < early_min:
                floor_stable_at_end = False # Minimum dropped late in the run

        # Step D: Adaptive / Robust Tolerance Check
        tolerance_us = 100
        is_converged = (min_spread <= tolerance_us) and floor_stable_at_end
        
        print(f"-> Bucket Mins (µs): {[round(m, 2) for m in bucket_mins]}")
        print(f"-> Bucket Min Spread: {round(min_spread, 2)} µs | Late Drop Check: {'Passed' if floor_stable_at_end else 'Failed'} | Floor Convergence: {'PASSED (Stable)' if is_converged else 'CHECK (High Variance/Late Drop)'}")

        # 2. Key Statistics Summary
        percentiles_list = [0.25, 0.50, 0.75, 0.90, 0.95, 0.99]
        summary = df[['anchor_measured_rtt_us', 'calibrated_distance_m']].describe(percentiles=percentiles_list)
        
        stem_name = Path(file_path).stem
        summary.to_csv(f"summary_{stem_name}.csv")
        
        rtt_stats = summary['anchor_measured_rtt_us']
        master_summary_list.append({
            'Test_Name': stem_name,
            'Count': int(rtt_stats['count']),
            'Mean_us': round(rtt_stats['mean'], 2),
            'Min_us': round(rtt_stats['min'], 2),
            'Bucket_Min_Spread_us': round(min_spread, 2),
            'Converged': is_converged,
            'P50_us': round(rtt_stats['50%'], 2),
            'P99_us': round(rtt_stats['99%'], 2)
        })
        
        # 3. Plotting
        fig, axes = plt.subplots(1, 2, figsize=(14, 5))

        axes[0].plot(df['seq'], df['anchor_measured_rtt_us'], marker='.', linestyle='none', alpha=0.3, color='dodgerblue', label='Raw RTT')
        axes[0].plot(df['seq'], df['running_min_us'], color='darkorange', linewidth=2, label='Expanding Min')
        axes[0].set_title(f'RTT & Min Convergence ({stem_name})')
        axes[0].set_xlabel('Sequence Number')
        axes[0].set_ylabel('RTT (µs)')
        axes[0].legend(loc='upper right')
        axes[0].grid(True)

        axes[1].hist(df['anchor_measured_rtt_us'], bins=30, color='mediumseagreen', alpha=0.7, edgecolor='black')
        axes[1].set_title('RTT Distribution')
        axes[1].set_xlabel('RTT (µs)')
        axes[1].set_ylabel('Frequency')
        axes[1].grid(True)

        plt.tight_layout()
        plt.savefig(f"rtt_analysis_{stem_name}.png", dpi=300)
        plt.close(fig)

    except Exception as e:
        print(f"Error processing {file_path}: {e}")

# Export master sheet with convergence validation column
if master_summary_list:
    master_df = pd.DataFrame(master_summary_list)
    master_df.to_csv("master_rtt_comparison_table.csv", index=False)
    print("\n" + "="*70)
    print("Master Table with Enhanced Convergence Results:")
    print("="*70)
    print(master_df.to_string(index=False))