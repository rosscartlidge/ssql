package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/rosscartlidge/ssql/v3"
)

func main() {
	fmt.Println("🎨 ssql Interactive Chart Demo")
	fmt.Println("=====================================")

	// Create output directory in /tmp
	outputDir := "/tmp/chart_demo"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatal(err)
	}

	// 1. Sales Performance Dashboard
	fmt.Println("📊 Creating Sales Performance Dashboard...")
	createSalesDashboard(outputDir)

	// 2. System Metrics Time Series
	fmt.Println("⏰ Creating System Metrics Time Series...")
	createSystemMetrics(outputDir)

	// 3. Process Analysis
	fmt.Println("🖥️  Creating Process Analysis Chart...")
	createProcessAnalysis(outputDir)

	// 4. Network Traffic Analysis
	fmt.Println("🌐 Creating Network Traffic Analysis...")
	createNetworkAnalysis(outputDir)

	// 5. Quick Chart Example
	fmt.Println("⚡ Creating Quick Chart Example...")
	createQuickExample(outputDir)

	fmt.Println("\n✅ All charts created successfully!")
	fmt.Println("\n🎯 Open these HTML files in your browser:")
	fmt.Printf("   📈 %s/sales_dashboard.html\n", outputDir)
	fmt.Printf("   📊 %s/system_metrics.html\n", outputDir)
	fmt.Printf("   🖥️  %s/process_analysis.html\n", outputDir)
	fmt.Printf("   🌐 %s/network_analysis.html\n", outputDir)
	fmt.Printf("   ⚡ %s/quick_example.html\n", outputDir)
	fmt.Println("\n🎪 Features to try:")
	fmt.Println("   • Click different chart types (line/bar/scatter/pie)")
	fmt.Println("   • Select different X and Y fields")
	fmt.Println("   • Toggle trend lines and moving averages")
	fmt.Println("   • Zoom and pan on the charts")
	fmt.Println("   • Switch between stacked/unstacked modes")
	fmt.Println("   • Export charts as PNG")
}

// createSalesDashboard demonstrates business analytics visualization
func createSalesDashboard(outputDir string) {
	// Generate realistic sales data
	salesData := []ssql.Record{}

	regions := []string{"North America", "Europe", "Asia Pacific", "Latin America"}
	products := []string{"Software Licenses", "Consulting", "Support", "Training"}

	baseDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for month := 0; month < 12; month++ {
		date := baseDate.AddDate(0, month, 0)

		for _, region := range regions {
			for _, product := range products {
				// Generate realistic sales patterns
				seasonality := 1.0 + 0.3*math.Sin(float64(month)*math.Pi/6) // Seasonal variation
				baseAmount := 50000 + rand.Float64()*100000                 // $50K-$150K base
				growth := 1.0 + float64(month)*0.02                         // 2% monthly growth
				noise := 0.8 + rand.Float64()*0.4                           // ±20% random variation

				sales := baseAmount * seasonality * growth * noise
				profit := sales * (0.15 + rand.Float64()*0.15) // 15-30% profit margin

				record := ssql.MakeMutableRecord().
					String("date", date.Format("2006-01-02")).
					String("region", region).
					String("product", product).
					Float("sales", sales).
					Float("profit", profit).
					Float("profit_margin", profit/sales*100).
					Int("deals_closed", int64(5+rand.Intn(20))).
					Freeze()

				salesData = append(salesData, record)
			}
		}
	}

	// Create interactive dashboard
	data := ssql.From(salesData)

	config := ssql.DefaultChartConfig()
	config.Title = "📊 Sales Performance Dashboard - Interactive Analytics"
	config.ChartType = "line"
	config.EnableCalculations = true
	config.EnableInteractive = true
	config.ColorScheme = "vibrant"
	config.Theme = "light"
	config.Width = 1400
	config.Height = 700

	if err := ssql.InteractiveChart(data, outputDir+"/sales_dashboard.html", config); err != nil {
		log.Fatalf("Error creating sales dashboard: %v", err)
	}
}

// createSystemMetrics demonstrates time series monitoring
func createSystemMetrics(outputDir string) {
	metricsData := []ssql.Record{}

	startTime := time.Now().Add(-2 * time.Hour)

	// Generate 5-minute interval system metrics
	for i := 0; i < 24; i++ { // 2 hours of 5-min intervals
		timestamp := startTime.Add(time.Duration(i) * 5 * time.Minute)

		// Simulate realistic system metrics with patterns
		baseLoad := 30.0 + 20.0*math.Sin(float64(i)*math.Pi/12) // Daily cycle
		cpuUsage := math.Max(0, math.Min(100, baseLoad+rand.Float64()*20-10))

		memoryBase := 60.0 + 15.0*math.Sin(float64(i)*math.Pi/8)
		memoryUsage := math.Max(0, math.Min(100, memoryBase+rand.Float64()*10-5))

		diskIO := 20 + rand.Float64()*80
		networkRx := 100 + rand.Float64()*500 // MB/s
		networkTx := 50 + rand.Float64()*200

		// Simulate some spikes
		if i == 8 || i == 15 {
			cpuUsage = math.Min(100, cpuUsage+30)
			memoryUsage = math.Min(100, memoryUsage+20)
		}

		record := ssql.MakeMutableRecord().
			String("timestamp", timestamp.Format("2006-01-02 15:04:05")).
			Float("cpu_usage", cpuUsage).
			Float("memory_usage", memoryUsage).
			Float("disk_io", diskIO).
			Float("network_rx_mbps", networkRx).
			Float("network_tx_mbps", networkTx).
			Float("load_average", cpuUsage/20).
			Int("active_connections", int64(100+rand.Intn(400))).
			Freeze()

		metricsData = append(metricsData, record)
	}

	data := ssql.From(metricsData)

	config := ssql.DefaultChartConfig()
	config.Title = "🖥️ System Metrics - Real-time Monitoring Dashboard"
	config.ChartType = "line"
	config.XAxisType = "time"
	config.EnableCalculations = true
	config.ColorScheme = "vibrant"
	config.Theme = "dark"
	config.Width = 1400
	config.Height = 700

	if err := ssql.TimeSeriesChart(data, "timestamp", []string{"cpu_usage", "memory_usage", "disk_io"}, outputDir+"/system_metrics.html", config); err != nil {
		log.Fatalf("Error creating system metrics: %v", err)
	}
}

// createProcessAnalysis demonstrates command output visualization
func createProcessAnalysis(outputDir string) {
	// Simulate ps command output
	processData := []ssql.Record{}

	users := []string{"root", "postgres", "nginx", "app", "monitoring"}
	commands := []string{
		"/usr/bin/postgres", "/usr/sbin/nginx", "/bin/systemd",
		"/usr/bin/python3", "/usr/bin/node", "/usr/sbin/sshd",
		"/usr/bin/redis-server", "/usr/bin/memcached",
	}

	for i := 0; i < 50; i++ {
		user := users[rand.Intn(len(users))]
		cmd := commands[rand.Intn(len(commands))]

		// Generate realistic process metrics
		var cpuUsage, memUsage float64
		var memSize int64

		switch {
		case user == "postgres":
			cpuUsage = 5 + rand.Float64()*15    // DB processes use more CPU
			memUsage = 200 + rand.Float64()*800 // MB
			memSize = int64(memUsage * 1024)    // KB
		case user == "nginx":
			cpuUsage = 1 + rand.Float64()*5
			memUsage = 10 + rand.Float64()*50
			memSize = int64(memUsage * 1024)
		default:
			cpuUsage = rand.Float64() * 10
			memUsage = 5 + rand.Float64()*100
			memSize = int64(memUsage * 1024)
		}

		record := ssql.MakeMutableRecord().
			Int("PID", int64(1000+i)).
			String("USER", user).
			Float("CPU", cpuUsage).
			Float("MEM", memUsage).
			Int("SZ", memSize).
			String("CMD", cmd).
			String("STAT", []string{"S", "R", "D", "T"}[rand.Intn(4)]).
			Int("TIME", int64(rand.Intn(3600))). // Seconds
			Freeze()

		processData = append(processData, record)
	}

	data := ssql.From(processData)

	config := ssql.DefaultChartConfig()
	config.Title = "🖥️ Process Analysis - CPU & Memory Usage"
	config.ChartType = "scatter"
	config.EnableInteractive = true
	config.ColorScheme = "pastel"
	config.Width = 1300
	config.Height = 600

	if err := ssql.InteractiveChart(data, outputDir+"/process_analysis.html", config); err != nil {
		log.Fatalf("Error creating process analysis: %v", err)
	}
}

// createNetworkAnalysis demonstrates network traffic visualization
func createNetworkAnalysis(outputDir string) {
	networkData := []ssql.Record{}

	protocols := []string{"TCP", "UDP", "ICMP"}
	ports := []int{22, 80, 443, 3306, 5432, 6379, 8080, 9090}

	baseTime := time.Now().Add(-1 * time.Hour)

	for minute := 0; minute < 60; minute++ {
		timestamp := baseTime.Add(time.Duration(minute) * time.Minute)

		for _, protocol := range protocols {
			for _, port := range ports {
				// Generate realistic network patterns
				var baseTraffic float64
				switch port {
				case 80, 443: // HTTP/HTTPS - high traffic
					baseTraffic = 1000 + rand.Float64()*5000
				case 22: // SSH - low but steady
					baseTraffic = 10 + rand.Float64()*50
				case 3306, 5432: // Database - medium traffic
					baseTraffic = 100 + rand.Float64()*500
				default:
					baseTraffic = 50 + rand.Float64()*200
				}

				// Add some spikes and patterns
				if minute >= 20 && minute <= 40 {
					baseTraffic *= 1.5 // Rush hour
				}

				if minute == 25 || minute == 35 {
					baseTraffic *= 2.5 // Traffic spikes
				}

				bytesIn := baseTraffic * (0.8 + rand.Float64()*0.4)
				bytesOut := baseTraffic * 0.6 * (0.8 + rand.Float64()*0.4)
				connections := int64(1 + rand.Intn(50))

				record := ssql.MakeMutableRecord().
					String("timestamp", timestamp.Format("2006-01-02 15:04:05")).
					String("protocol", protocol).
					Int("port", int64(port)).
					Float("bytes_in", bytesIn).
					Float("bytes_out", bytesOut).
					Float("total_bytes", bytesIn+bytesOut).
					Int("connections", connections).
					Float("avg_response_time", 50+rand.Float64()*200). // ms
					Freeze()

				networkData = append(networkData, record)
			}
		}
	}

	data := ssql.From(networkData)

	config := ssql.DefaultChartConfig()
	config.Title = "🌐 Network Traffic Analysis - Bytes & Connections"
	config.ChartType = "line"
	config.XAxisType = "time"
	config.EnableCalculations = true
	config.EnableInteractive = true
	config.ColorScheme = "monochrome"
	config.Theme = "light"
	config.Width = 1400
	config.Height = 650

	if err := ssql.TimeSeriesChart(data, "timestamp", []string{"bytes_in", "bytes_out", "connections"}, outputDir+"/network_analysis.html", config); err != nil {
		log.Fatalf("Error creating network analysis: %v", err)
	}
}

// createQuickExample demonstrates the simple API
func createQuickExample(outputDir string) {
	// Simple monthly revenue data
	revenueData := []ssql.Record{
		ssql.MakeMutableRecord().String("month", "Jan 2024").Float("revenue", 120000).Int("customers", 450).Float("avg_deal", 2667).Freeze(),
		ssql.MakeMutableRecord().String("month", "Feb 2024").Float("revenue", 135000).Int("customers", 480).Float("avg_deal", 2813).Freeze(),
		ssql.MakeMutableRecord().String("month", "Mar 2024").Float("revenue", 118000).Int("customers", 425).Float("avg_deal", 2776).Freeze(),
		ssql.MakeMutableRecord().String("month", "Apr 2024").Float("revenue", 142000).Int("customers", 510).Float("avg_deal", 2784).Freeze(),
		ssql.MakeMutableRecord().String("month", "May 2024").Float("revenue", 156000).Int("customers", 545).Float("avg_deal", 2862).Freeze(),
		ssql.MakeMutableRecord().String("month", "Jun 2024").Float("revenue", 148000).Int("customers", 520).Float("avg_deal", 2846).Freeze(),
	}

	data := ssql.From(revenueData)

	// Use the simple QuickChart API
	if err := ssql.QuickChart(data, "month", "revenue", outputDir+"/quick_example.html"); err != nil {
		log.Fatalf("Error creating quick example: %v", err)
	}
}
