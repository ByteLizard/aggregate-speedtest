export namespace main {
	
	export class Server {
	    id: number;
	    name: string;
	    location: string;
	    country: string;
	
	    static createFrom(source: any = {}) {
	        return new Server(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.location = source["location"];
	        this.country = source["country"];
	    }
	}

}

