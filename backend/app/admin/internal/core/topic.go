package core

const (
	SYS_RUNTIME                  = "sysRuntime"                // 系统运行日志  topic
	LOGINLOG                     = "loginLog"                  // 系统登录日志
	SUB_SnmpMonitor_MonitorData  = "snmpMonitor.monitorData"   // snmp监控数据  topic
	PUB_SnmpMonitor_Cmd          = "snmpMonitor.cmd"           //snmp收到的发送数据命令
	PUB_ServiceMonitor_Cmd       = "serviceMonitor.cmd"        //serviceMonitor发送的关闭服务命令
	SUB_ServiceMonitor_CmdResult = "serviceMonitor.cmdResult"  //相应命令结果
	SUB_SnmpMonitor_DevData      = "snmpMonitor.devData"       // snmp发送设备数据
	SUB_SnmpMonitor_DevList      = "snmpMonitor.devList"       // snmp发送设备概要数据列表
	SUB_SroomMonitor_DevData     = "snmpMonitor.sroom.devData" // snmp发送设备数据
	PUB_SroomMonitor_Cmd         = "snmpMonitor.sroom.cmd"     //snmp发送是否开启机房的命令

	SUB_ServiceMonitor_MonitorData = "serviceMonitor.monitorData" // service监控数据  topic
	SUB_ServiceMonitor_Monitor     = "serviceMonitor.monitorcc"   //修改结构，获取服务监控数据
	SUB_ServiceMonitor_Client      = "serviceMonitor.client"      //client端监控的结果

	SUB_NatsMonitor_MonitorData = "natsMonitor.monitorData" // nats及discovery监控数据  topic
	SUB_DataForward_alarm       = "dataForward.alarm"       //数据转发服务 告警
	PUB_DataForward_cmd         = "dataForward.cmd"         //数据转发服务 命令
	SUB_DataForward_sendData    = "dataForward.sendData"    //数据转发 转发数据查看
	SUB_DataForward_sendStatus  = "dataForward.sendStatus"  //数据转发 输出状态
	PUB_DataForward_statusCmd   = "dataForward.statusCmd"   //数据转发 查看状态命令
	SUB_Linemonitor_Ping        = "linemonitor.ping"        //线路监控ping的状态信息
	SUB_ObsBook_monthData       = "obsbook.month"           //月总簿数据发送
	SUB_ObsBook_yearData        = "obsbook.year"            //年总簿数据发送
	SUB_ObsBook_message         = "obsbook.message"         //月总簿制作过程中的问题数据
	SUB_ObsBook_yearMessage     = "obsbook.yearmessage"     //年总簿制作过程中的问题数据
	SUB_Alarm_Warning           = "alarm.warning"           // 发送给前端的告警

	//devstatus
	SUB_Alarm_Warning_Station = "site.status"   // 站点告警信息 发送给前端的告警
	SUB_Alarm_Warning_Device  = "device.status" // 设备告警信息 发送给前端的告警
	SUB_DEVICE_ORIGINAL       = "Data.original" // 原始数据推送

	SUB_DBmonitorGY_GetRadarAndSatelliteCount    = "DBmonitorGY.RAndSCount"        //DBmonitorGY的当日气象雷达卫星产品数量
	SUB_DBmonitorGY_GetMeteInfoFileCount         = "DBmonitorGY.FileCount"         //DBmonitorGY的当日气象服务产品数量
	SUB_DBmonitorGY_GetMeteInfoQueryCount        = "DBmonitorGY.QueryCount"        //DBmonitorGY的当日气象信息查询数量
	SUB_DBmonitorGY_GetLastRadarAndSatelliteTime = "DBmonitorGY.LastRAndSTime"     //DBmonitorGY的最新气象雷达卫星产品时间
	SUB_DBmonitorGY_GetLastMeteInfoFileTime      = "DBmonitorGY.LastFileTime"      //DBmonitorGY的最新气象服务产品时间
	SUB_DBmonitorGY_GetLastMeteInfoQueryTime     = "DBmonitorGY.LastQueryTime"     //DBmonitorGY的最新气象信息查询时间
	SUB_DBmonitorGY_ListProduct                  = "DBmonitorGY.ListProduct"       //分页查询最新的  气象服务产品和气象信息查询
	SUB_DockerListMonitor_MonitorData            = "dockermonitor.listmonitorData" // docker监控数据  topic
	SUB_DockerMonitor_MonitorData                = "dockermonitor.monitorData"     // docker监控数据  topic
	SUB_DockerMonitor_Service_ListMonitorData    = "dockermonitor.servicelistData" // docker监控数据  topic
	SUB_DockerMonitor_Service_MonitorData        = "dockermonitor.serviceData"     // docker监控数据  topic

	SUB_REPORT_REPORTMONITOR_LIST  = "reportMonitor.listmonitor"  //reportMonitor 最新报文监控列表
	SUB_REPORTMONITOR_ReportStatus = "reportMonitor.reportstatus" //发送最新的告警数据给前端

	// 首页告警推送
	SUB_MONITOR_INDEX = "monitor.index.push" //监控界面首页推送给首页显示的告警

	// 雷达参数 前端
	SUB_RADARPARAM_NOTIFY = "radar.param.notify"
	SUB_monitor_Devlist   = "prometheusMonitor.Devlist" // 推送当前设备列表前五的数据
)
