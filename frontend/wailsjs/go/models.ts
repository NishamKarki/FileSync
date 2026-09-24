export namespace fsnotify {
	
	export class Event {
	    Name: string;
	    Op: number;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Op = source["Op"];
	    }
	}

}

export namespace main {
	
	export class Chunk {
	    Index: number;
	    Data: number[];
	
	    static createFrom(source: any = {}) {
	        return new Chunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Index = source["Index"];
	        this.Data = source["Data"];
	    }
	}

}

export namespace network {
	
	export class PingResponse {
	    success: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new PingResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	    }
	}

}

