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

def parse_filename_metadata(stem):
    """
    Parses filenames like 'measurements_falk_to_node1_gpu' or 'measurements_tuusula_to_tuusula_cpu'
    into human-readable Anchor, Attester, and Mode fields including their proper labels and IPs.
    """
    # Remove prefix if present
    name = stem.replace("measurements_", "")
    
    # Determine mode (cpu vs gpu)
    mode = "CPU"
    if name.endswith("_gpu"):
        mode = "GPU"
        name = name[:-4]
    elif name.endswith("_cpu"):
        mode = "CPU"
        name = name[:-4]
        
    # Split by 'to'
    parts = name.split("_to_")
    if len(parts) == 2:
        raw_anchor, raw_attester = parts
    else:
        raw_anchor, raw_attester = "Unknown", "Unknown"
        
    # Map node identifiers to clean names with labels and IPs matching your format
    node_mapping = {
        "falk": "Falkenstein 49.13.51.115",
        "node1": "Cambridge Node 1: 192.168.80.80",
        "tuusula": "Tuusula: 37.27.88.255"
    }
    
    anchor_name = node_mapping.get(raw_anchor.lower(), raw_anchor)
    attester_name = node_mapping.get(raw_attester.lower(), raw_attester)
    
    return anchor_name, attester_name, mode

# --- DYNAMIC DIRECTORY LOADING ---
results_dir = Path("../results_20260821_104000")
files_to_load = sorted(list(results_dir.glob("measurements_*.json")))

if not files_to_load:
    print(f"No measurement files found in {results_dir.absolute()}")
else:
    print(f"Found {len(files_to_load)} measurement files to process.")

master_summary_list = []

for file_path in files_to_load:
    try:
        print(f"\nProcessing: {file_path.name}")
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
        warmup_cutoff = 30
        df_eval = df.iloc[warmup_cutoff:].copy() if len(df) > warmup_cutoff else df.copy()

        num_buckets = 10
        rtt_values = df_eval['anchor_measured_rtt_us'].values
        chunks = np.array_split(rtt_values, num_buckets)
        bucket_mins = [chunk.min() for chunk in chunks if len(chunk) > 0]
        
        min_spread = max(bucket_mins) - min(bucket_mins) if bucket_mins else 0.0
        
        df['running_min_us'] = df['anchor_measured_rtt_us'].expanding().min()
        split_idx = int(len(df) * 0.9)
        floor_stable_at_end = True
        if len(df) > 10 and split_idx > 0:
            early_min = df['running_min_us'].iloc[split_idx]
            final_min = df['running_min_us'].iloc[-1]
            if final_min < early_min:
                floor_stable_at_end = False

        tolerance_us = 100
        is_converged = (min_spread <= tolerance_us) and floor_stable_at_end

        # 2. Key Statistics Summary
        percentiles_list = [0.25, 0.50, 0.75, 0.90, 0.95, 0.99]
        summary = df[['anchor_measured_rtt_us', 'calibrated_distance_m']].describe(percentiles=percentiles_list)
        
        stem_name = file_path.stem
        summary.to_csv(f"summary_{stem_name}.csv")
        
        rtt_stats = summary['anchor_measured_rtt_us']
        count = int(rtt_stats['count'])
        mean_val = round(rtt_stats['mean'], 2)
        min_val = round(rtt_stats['min'], 2)
        p50_val = round(rtt_stats['50%'], 2)
        p99_val = round(rtt_stats['99%'], 2)
        p99_minus_min = round(p99_val - min_val, 2)
        
        anchor_lbl, attester_lbl, mode_lbl = parse_filename_metadata(stem_name)

        master_summary_list.append({
            'Anchor': anchor_lbl,
            'Attester': attester_lbl,
            'CPU/GPU': mode_lbl,
            'Count': count,
            'Mean us': mean_val,
            'Min us': min_val,
            'Min Spread us': round(min_spread, 2),
            'Converged': is_converged,
            '50th Percentile us': p50_val,
            '99th Percentile us': p99_val,
            'p99 - min': p99_minus_min
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
        print(f"Error processing {file_path.name}: {e}")

# Export master sheet with exact column layout
if master_summary_list:
    master_df = pd.DataFrame(master_summary_list)
    
    # Reorder columns explicitly to match your layout requirement
    desired_columns = [
        'Anchor', 'Attester', 'CPU/GPU', 'Count', 'Mean us', 
        'Min us', 'Min Spread us', 'Converged', 
        '50th Percentile us', '99th Percentile us', 'p99 - min'
    ]
    master_df = master_df[desired_columns]
    
    master_df.to_csv("master_rtt_comparison_table.csv", index=False)
    print("\n" + "="*100)
    print("Master Table with Enhanced Convergence Results:")
    print("="*100)
    print(master_df.to_string(index=False))