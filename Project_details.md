# 🔐 Zero Trust Network Access (ZTNA) with Load Balancing & NIDS Integration

## Description
This project implements a Zero Trust Architecture (ZTA) for a multi-server system that can handle a large user base efficiently while ensuring strong security. The system integrates load balancing, Zero Trust verification, and a Network Intrusion Detection System (NIDS) to protect against malicious traffic, including Denial-of-Service (DoS) attacks.

---

## Key Features

### Zero Trust Access Control
- Every request is authenticated and authorized before accessing resources.  
- Continuous verification instead of relying on a single login.  
- Enforces least-privilege access and micro-segmentation of services.  

### Load Balancing & Traffic Distribution
- A load balancer intelligently distributes traffic across multiple servers.  
- Prevents server overload and ensures high availability.  
- Monitors server health to reroute requests in case of failure.  

### Network Intrusion Detection System (NIDS)
- Monitors network traffic for malicious patterns (e.g., port scanning, DoS attempts, unusual request rates).  
- Generates real-time alerts when suspicious activity is detected.  
- Can optionally integrate Machine Learning models to detect anomalies and zero-day attacks.  

### DoS Attack Handling
- The system can simulate DoS attacks for testing resilience.  
- Load balancer + NIDS work together to filter and drop malicious traffic.  

### Scalability & Real-World Usability
- Suitable for real-world scenarios like enterprise networks, SaaS platforms, or API-driven services.  
- Can be deployed in a cloud-based environment or on-premises lab setup.  

---

## Tech Stack (Suggested)
- **Networking:** Linux IPTables, HAProxy / Nginx (for load balancing), WireGuard / OpenVPN (for secure tunnels)  
- **Zero Trust:** OAuth 2.0 / JWT, Identity-Aware Proxy, role-based access policies  
- **NIDS:** Snort / Suricata (signature-based), or Python + Scikit-learn for anomaly detection  
- **DoS Simulation:** Custom traffic generator with Scapy or hping3  
- **Monitoring & Visualization:** ELK Stack (Elasticsearch, Logstash, Kibana) or Grafana  

---

## Deliverables
- Working prototype of a Zero Trust load-balanced network.  
- Live dashboard showing traffic distribution, security alerts, and attack detection.  
- Documentation of design, architecture, and test cases (including simulated DoS attacks).  

---

# 🔗 How to Connect Your ML IDS into the Zero Trust + NIDS Setup

## 1. System Flow Integration

### Traffic Capture
- Gateway or NIDS captures flow-level features (not raw packets).  
- Example: Extract packet counts, duration, bytes, etc. (similar to UNSW-NB15 features).  

### Feature Preprocessing
- Apply same preprocessing as your ML pipeline: scaling, encoding, etc.  
- This ensures live traffic is in the same format as training data.  

### ML Hybrid Detection
1. **Step A:** Run clustering (KMeans/DBSCAN) to assign a cluster ID or anomaly score.  
2. **Step B:** Feed cluster-enriched data into your classifier (Random Forest/XGBoost).  

**Output:** Probability of traffic being malicious.  

### Response Decision
- If classified as normal → forward to backend via load balancer.  
- If classified as attack → enforce Zero Trust response (block, rate-limit, alert).  

---

## 2. Where ML Sits in the Pipeline
**Architecture with ML added:**


- **Gateway:** Still enforces Zero Trust authentication.  
- **ML IDS:** Processes traffic features (in real-time batch or streaming mode).  
- **Response Engine:** Acts based on ML’s verdict (block/drop/rate-limit).  

---

## 3. Why Hybrid ML Helps Here
- **Traditional NIDS:** Rule/signature-based → good for known attacks, weak for zero-days.  
- **ML Hybrid IDS:**
  - Clustering discovers hidden/unknown attack patterns.  
  - Classification ensures high accuracy on known attacks.  
  - Together → reduces false positives and increases detection of novel threats.  

**Demo Approach:**
- Run signature-based detection (baseline), e.g., Snort-style rule: “100 SYN/sec → DoS”.  
- Run ML IDS detection in parallel.  
- Show how ML catches traffic that rules miss (or reduces false alarms).  

---

## 4. Implementation Strategy

### Offline (Model Training)
- Train on UNSW-NB15 / CICIDS2017 dataset.  
- Save trained clustering + classifier pipeline (using joblib or pickle).  

### Online (Deployment)
- Traffic capture module extracts flow features (from live packets or replayed PCAPs).  
- Features → Preprocessor → ML pipeline → Prediction.  
- Prediction → Action (forward/drop/alert).  

---

## 5. Final Deliverables (Combined Project)
- Zero Trust Gateway + Load Balancer (core networking).  
- NIDS Integration (rule-based + ML-enhanced).  
- ML IDS Engine (clustering + classification hybrid).  
- **Dashboard:**  
  - Traffic metrics (normal vs suspicious).  
  - Alerts flagged by ML IDS.  
  - Comparison: classification-only vs hybrid.  
- **DoS Simulation Testbed:** Generate attack traffic, measure detection rates.
