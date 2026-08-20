```mermaid
sequenceDiagram
participant Attester as Attester (GPU / CPU Node)
participant Anchor as Anchor (Known Coordinates)

    Note over Attester,Anchor: Phase 1: Probe 0 (Setup & Nonce Request)
    Attester->>Anchor: Send Probe 0 (Unchallenged)
    
    Anchor-->>Attester: Reply 0 (Contains Random Nonce & Signed Offset)

    Note over Anchor: [Anchor Clock Starts Timing]<br/>AnchorMeasuredRttNs timer begins here!
    

    Note over Attester: 1. Extract Nonce from Reply 0<br/>2. Run VRAM challenge (if -use-gpu)<br/>3. Sign Probe 1 echoing the Nonce

    Note over Attester,Anchor: Phase 2: Probe 1 (The Load-Bearing Challenge)
    Attester->>Anchor: Send Probe 1 (Echoes Nonce & Carries Signature)

    Note over Anchor: [Anchor Clock Stops Timing]<br/>Measures interval since Reply 0 was sent.

    Anchor->>Attester: Reply 1 (Contains AnchorMeasuredRttNs & Signature)

    Note over Attester: Final calculation: converts RTT to<br/>ProvableMaxDistanceM & CalibratedDistanceM