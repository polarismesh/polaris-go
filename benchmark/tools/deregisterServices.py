# -*- coding: UTF-8 -*-

import sys
import string
import requests
import json

namespace = "Test"
targetServicePattern = "goPerfService_%d_%d"
instancePattern = "10.29.33.%d"
tokens = {
    'goPerfService_100_10': 'c85294e77ce8412a942d3cac239d726e',
    'goPerfService_100_100': 'e6983aa0916b489a9c850a8aa9c613f2',
    'goPerfService_100_200': '656207aace3441a4843dd3dcd4fb1829'
}


def main():
    if len(sys.argv) < 4:
        print('using %s <server_address> <instance_count> <metadata_count>\n' % sys.argv[0])
        sys.exit(1)
    serverAddr = sys.argv[1]
    instanceCount = string.atoi(sys.argv[2])
    metadataCount = string.atoi(sys.argv[3])
    print 'serverAddr is %s, instanceCount is %d\n' % (serverAddr, instanceCount)
    targetUrl = 'http://%s/v1/DeregisterInstance' % serverAddr
    targetService = targetServicePattern % (instanceCount, metadataCount)
    for i in range(0, instanceCount):
        host = instancePattern % (i+1)
        data = {
                   'service_token': '%s' % tokens[targetService],
                   'service': targetService,
                   'namespace': namespace,
                   'host': host,
                   'port': 6520
                }
        resp = requests.post(targetUrl, headers={"content-type": "application/json"}, json=data)
        if resp.status_code != 200:
            print 'fail to deregister instance %s, code is %d, text is %s\n' % (host, resp.status_code, resp.text)
            sys.exit(2)
        respObj = json.loads(resp.text)
        if respObj['code'] != 200000 and respObj['code'] != 400202:
            print 'fail to deregister instance %s, server code is %d, info is %s\n' % (host, respObj['code'], respObj['info'])


if __name__ == "__main__":
    main()