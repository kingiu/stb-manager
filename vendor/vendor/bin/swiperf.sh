#!/vendor/bin/sh

swiperf_cmd=`getprop sys.net.swiperf.cmd`
echo ${swiperf_cmd}
#log -t AAA '${swiperf_cmd}'

swiperf ${swiperf_cmd}
